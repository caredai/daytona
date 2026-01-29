package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	daytona "github.com/daytonaio/daytona/libs/api-client-go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Config holds the configuration for the runner-manager
type Config struct {
	APIPort                       string
	DaytonaAPIURL                 string
	DaytonaAPIKey                 string
	ProviderNamespace             string
	RegionID                      string
	MaxResourceUtilizationPercent int
	MinIdleRunners                int
	MinIdleCpu                    int
	MinIdleMemory                 int
}

// ClusterState represents the current state of the cluster
type ClusterState struct {
	Runners          []daytona.RunnerFull
	ActiveRunners    []daytona.RunnerFull
	DeletableRunners []daytona.RunnerFull
	IdleRunners      []daytona.RunnerFull

	RunnerByDomain map[string]daytona.RunnerFull // Maps runner domain (IP) to runner

	PendingPlaceholders   []*corev1.Pod
	ScheduledPlaceholders []*corev1.Pod

	Nodes        []corev1.Node           // All nodes
	NodeByIP     map[string]*corev1.Node // Maps node IP to node
	NascentNodes []*corev1.Node          // Nodes with scheduled placeholders but no runner yet
}

// ResourceMetrics holds aggregated resource metrics
type ResourceMetrics struct {
	TotalCPUCapacity        float32
	TotalMemoryGiBCapacity  float32
	TotalAllocatedCPU       float32
	TotalAllocatedMemoryGiB float32
	TotalAvailableCPU       float32
	TotalAvailableMemoryGiB float32
	AvgCpuPerNode           float32
	AvgMemPerNode           float32
}

const (
	// CheckInterval defines how often the controller loop runs
	CheckInterval = 30 * time.Second

	// PlaceholderPodLabel is the label for naming placeholder pods
	PlaceholderPodLabel = "daytona-runner-placeholder"

	// NodeSelectorKey and TaintKey are constants for Kubernetes node selection
	NodeSelectorKey = "daytona-sandbox-c"
	TaintKey        = "sandbox"
)

// main function to start the runner-manager
func main() {
	log.Println("Starting runner-manager...")

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	apiClient, err := initializeDaytonaClient(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize Daytona API client: %v", err)
	}

	clientset, err := initializeKubernetesClient()
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	startHealthCheckServer(cfg.APIPort)

	runControllerLoop(cfg, apiClient, clientset)
}

// loadConfig reads and validates configuration from environment variables
func loadConfig() (*Config, error) {
	cfg := &Config{}

	cfg.APIPort = os.Getenv("API_PORT")
	if cfg.APIPort == "" {
		return nil, fmt.Errorf("environment variable API_PORT not set")
	}

	cfg.DaytonaAPIURL = os.Getenv("DAYTONA_API_URL")
	if cfg.DaytonaAPIURL == "" {
		return nil, fmt.Errorf("environment variable DAYTONA_API_URL not set")
	}

	cfg.DaytonaAPIKey = os.Getenv("DAYTONA_API_KEY")
	if cfg.DaytonaAPIKey == "" {
		return nil, fmt.Errorf("environment variable DAYTONA_API_KEY not set")
	}

	cfg.ProviderNamespace = os.Getenv("PROVIDER_NAMESPACE")
	if cfg.ProviderNamespace == "" {
		return nil, fmt.Errorf("environment variable PROVIDER_NAMESPACE not set")
	}

	cfg.RegionID = os.Getenv("REGION_ID")
	if cfg.RegionID == "" {
		return nil, fmt.Errorf("environment variable REGION_ID not set")
	}

	maxResourceUtilizationPercentStr := os.Getenv("MAX_RESOURCE_UTILIZATION_PERCENT")
	if maxResourceUtilizationPercentStr == "" {
		return nil, fmt.Errorf("environment variable MAX_RESOURCE_UTILIZATION_PERCENT not set")
	}
	var err error
	cfg.MaxResourceUtilizationPercent, err = strconv.Atoi(maxResourceUtilizationPercentStr)
	if err != nil {
		return nil, fmt.Errorf("invalid MAX_RESOURCE_UTILIZATION_PERCENT: %v", err)
	}
	if cfg.MaxResourceUtilizationPercent < 0 || cfg.MaxResourceUtilizationPercent > 100 {
		return nil, fmt.Errorf("MAX_RESOURCE_UTILIZATION_PERCENT must be between 0 and 100")
	}

	minIdleRunnersStr := os.Getenv("MIN_IDLE_RUNNERS")
	if minIdleRunnersStr == "" {
		return nil, fmt.Errorf("environment variable MIN_IDLE_RUNNERS not set")
	}
	cfg.MinIdleRunners, err = strconv.Atoi(minIdleRunnersStr)
	if err != nil {
		return nil, fmt.Errorf("invalid MIN_IDLE_RUNNERS: %v", err)
	}
	if cfg.MinIdleRunners < 0 {
		return nil, fmt.Errorf("MIN_IDLE_RUNNERS cannot be negative")
	}

	minIdleCpuStr := os.Getenv("MIN_IDLE_CPU")
	if minIdleCpuStr == "" {
		return nil, fmt.Errorf("environment variable MIN_IDLE_CPU not set")
	}
	cfg.MinIdleCpu, err = strconv.Atoi(minIdleCpuStr)
	if err != nil {
		return nil, fmt.Errorf("invalid MIN_IDLE_CPU: %v", err)
	}
	if cfg.MinIdleCpu < 0 {
		return nil, fmt.Errorf("MIN_IDLE_CPU cannot be negative")
	}

	minIdleMemoryStr := os.Getenv("MIN_IDLE_MEMORY")
	if minIdleMemoryStr == "" {
		return nil, fmt.Errorf("environment variable MIN_IDLE_MEMORY not set")
	}
	cfg.MinIdleMemory, err = strconv.Atoi(minIdleMemoryStr)
	if err != nil {
		return nil, fmt.Errorf("invalid MIN_IDLE_MEMORY: %v", err)
	}
	if cfg.MinIdleMemory < 0 {
		return nil, fmt.Errorf("MIN_IDLE_MEMORY cannot be negative")
	}

	return cfg, nil
}

// initializeDaytonaClient creates and configures the Daytona API client
func initializeDaytonaClient(cfg *Config) (*daytona.APIClient, error) {
	apiCfg := daytona.NewConfiguration()
	apiCfg.DefaultHeader = map[string]string{
		"Authorization": "Bearer " + cfg.DaytonaAPIKey,
	}
	apiCfg.Servers = daytona.ServerConfigurations{
		{
			URL: cfg.DaytonaAPIURL,
		},
	}
	return daytona.NewAPIClient(apiCfg), nil
}

// initializeKubernetesClient creates and configures the Kubernetes client
func initializeKubernetesClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Println("Falling back to kubeconfig due to error:", err)
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("error building kubeconfig: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating Kubernetes clientset: %w", err)
	}

	return clientset, nil
}

// startHealthCheckServer starts the health check HTTP server
func startHealthCheckServer(apiPort string) {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	go func() {
		log.Printf("Health check server listening on :%s", apiPort)
		if err := http.ListenAndServe(":"+apiPort, nil); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Could not start health check server: %v", err)
		}
	}()
}

// runControllerLoop runs the main controller loop
func runControllerLoop(cfg *Config, apiClient *daytona.APIClient, clientset *kubernetes.Clientset) {
	ticker := time.NewTicker(CheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Running controller loop...")

		state, err := gatherClusterState(apiClient, clientset, cfg.RegionID, cfg.ProviderNamespace)
		if err != nil {
			log.Printf("Error gathering cluster state: %v", err)
			continue
		}

		metrics := calculateResourceMetrics(state)

		logClusterState(state, metrics)

		needsScaleUp := shouldScaleUp(metrics, cfg, len(state.IdleRunners), len(state.NascentNodes))
		if needsScaleUp {
			if handleScaleUp(clientset, cfg, state, metrics) {
				continue // Skip scale-down logic for this cycle
			}
		}

		handleScaleDown(clientset, cfg, state, metrics, needsScaleUp)
	}
}

// gatherClusterState collects all cluster state information from various sources
func gatherClusterState(apiClient *daytona.APIClient, clientset *kubernetes.Clientset, regionID, providerNamespace string) (*ClusterState, error) {
	state := &ClusterState{
		RunnerByDomain: make(map[string]daytona.RunnerFull),
		NodeByIP:       make(map[string]*corev1.Node),
	}

	// Fetch runners from Daytona API
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := apiClient.AdminAPI.AdminListRunners(ctx).RegionId(regionID)
	runners, _, err := req.Execute()
	if err != nil {
		return nil, fmt.Errorf("failed to list runners from Daytona API: %w", err)
	}
	state.Runners = runners

	// Categorize runners and build domain-based mapping
	for _, runner := range state.Runners {
		domain := runner.GetDomain()
		if domain != "" {
			state.RunnerByDomain[domain] = runner
		}

		isAllocated := (runner.GetCurrentAllocatedCpu() > 0) ||
			(runner.GetCurrentAllocatedMemoryGiB() > 0) ||
			(runner.GetCurrentAllocatedDiskGiB() > 0) ||
			(runner.GetCurrentStartedSandboxes() > 0) ||
			(runner.GetCurrentSnapshotCount() > 0)

		if isAllocated {
			state.ActiveRunners = append(state.ActiveRunners, runner)
		} else if runner.GetUnschedulable() {
			state.DeletableRunners = append(state.DeletableRunners, runner)
		} else {
			state.IdleRunners = append(state.IdleRunners, runner)
		}
	}

	// Fetch placeholder pods
	allPlaceholders, err := clientset.CoreV1().Pods(providerNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=" + PlaceholderPodLabel,
	})
	if err != nil {
		return nil, fmt.Errorf("error listing placeholder pods: %w", err)
	}

	// Categorize placeholders
	for i := range allPlaceholders.Items {
		pod := &allPlaceholders.Items[i]
		if pod.Spec.NodeName == "" {
			state.PendingPlaceholders = append(state.PendingPlaceholders, pod)
		} else {
			state.ScheduledPlaceholders = append(state.ScheduledPlaceholders, pod)
		}
	}

	// Fetch K8s nodes
	nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: NodeSelectorKey + "=true",
	})
	if err != nil {
		return nil, fmt.Errorf("error listing K8s nodes: %w", err)
	}
	state.Nodes = nodes.Items

	// Build node IP mapping
	for i := range state.Nodes {
		node := &state.Nodes[i]
		nodeIPs := extractNodeIPs(node)
		for _, ip := range nodeIPs {
			state.NodeByIP[ip] = node
		}
	}

	// Identify nascent nodes (nodes with scheduled placeholders but no runner yet)
	for _, node := range state.Nodes {
		if node.Spec.Unschedulable {
			continue
		}
		// Check if node has a runner
		hasRunner := false
		nodeIPs := extractNodeIPs(&node)
		for _, ip := range nodeIPs {
			if _, found := state.RunnerByDomain[ip]; found {
				hasRunner = true
				break
			}
		}
		// If no runner but has scheduled placeholder, it's nascent
		if !hasRunner {
			for _, pod := range state.ScheduledPlaceholders {
				if pod.Spec.NodeName == node.Name {
					state.NascentNodes = append(state.NascentNodes, &node)
					break
				}
			}
		}
	}

	return state, nil
}

// calculateResourceMetrics calculates aggregated resource metrics
// Priority: Use runner-reported capacity when available, fallback to K8s node capacity for nodes without runners
func calculateResourceMetrics(state *ClusterState) *ResourceMetrics {
	metrics := &ResourceMetrics{}

	// Track which nodes have runners (by node name)
	nodesWithRunners := make(map[string]bool)

	// Calculate total capacity: prioritize runner-reported capacity (from Docker, more accurate)
	for _, runner := range state.Runners {
		if !runner.GetUnschedulable() {
			// Use runner-reported capacity (from Docker, more accurate)
			metrics.TotalCPUCapacity += runner.GetCpu()
			metrics.TotalMemoryGiBCapacity += runner.GetMemory()
			// Track which nodes have runners
			domain := runner.GetDomain()
			if domain != "" {
				if node, found := state.NodeByIP[domain]; found {
					nodesWithRunners[node.Name] = true
				}
			}
		}
	}

	// Add capacity from nodes without runners using K8s allocatable resources (fallback)
	// This includes nascent nodes and any other nodes that don't have runners yet
	for _, node := range state.Nodes {
		if node.Spec.Unschedulable {
			continue
		}
		// Skip nodes that already have runners (we used runner-reported capacity above)
		if nodesWithRunners[node.Name] {
			continue
		}
		// Use K8s allocatable resources as fallback
		nodeCpu, nodeMem, err := getNodeAllocatableResources(&node)
		if err != nil {
			log.Printf("Warning: Could not get allocatable resources for node %s: %v", node.Name, err)
			continue
		}
		metrics.TotalCPUCapacity += nodeCpu
		metrics.TotalMemoryGiBCapacity += nodeMem
	}

	// Calculate allocated resources from runners (always from runner data)
	for _, runner := range state.ActiveRunners {
		if allocatedCPU, ok := runner.GetCurrentAllocatedCpuOk(); ok && allocatedCPU != nil {
			metrics.TotalAllocatedCPU += *allocatedCPU
		}
		if allocatedMemory, ok := runner.GetCurrentAllocatedMemoryGiBOk(); ok && allocatedMemory != nil {
			metrics.TotalAllocatedMemoryGiB += *allocatedMemory
		}
	}

	// Calculate available resources
	metrics.TotalAvailableCPU = metrics.TotalCPUCapacity - metrics.TotalAllocatedCPU
	metrics.TotalAvailableMemoryGiB = metrics.TotalMemoryGiBCapacity - metrics.TotalAllocatedMemoryGiB

	// Calculate average node capacity based on all schedulable nodes
	schedulableNodeCount := 0
	for _, node := range state.Nodes {
		if !node.Spec.Unschedulable {
			schedulableNodeCount++
		}
	}
	if schedulableNodeCount > 0 {
		metrics.AvgCpuPerNode = metrics.TotalCPUCapacity / float32(schedulableNodeCount)
		metrics.AvgMemPerNode = metrics.TotalMemoryGiBCapacity / float32(schedulableNodeCount)
	}

	return metrics
}

// logClusterState logs the current cluster state
func logClusterState(state *ClusterState, metrics *ResourceMetrics) {
	log.Printf("Current state: DaytonaRunners: %d (Active: %d, Idle: %d, Deletable: %d). Nodes in pool: %d. NascentNodes: %d. Placeholders: %d (Pending: %d, Scheduled: %d).",
		len(state.Runners), len(state.ActiveRunners), len(state.IdleRunners), len(state.DeletableRunners),
		len(state.Nodes), len(state.NascentNodes), len(state.PendingPlaceholders)+len(state.ScheduledPlaceholders),
		len(state.PendingPlaceholders), len(state.ScheduledPlaceholders))
	log.Printf("Aggregated Capacity: CPU=%.2f, Mem=%.2fGiB. Aggregated Allocated: CPU=%.2f, Mem=%.2fGiB. Aggregated Available: CPU=%.2f, Mem=%.2fGiB.",
		metrics.TotalCPUCapacity, metrics.TotalMemoryGiBCapacity, metrics.TotalAllocatedCPU, metrics.TotalAllocatedMemoryGiB,
		metrics.TotalAvailableCPU, metrics.TotalAvailableMemoryGiB)
	log.Printf("Average node capacity: CPU=%.2f, Mem=%.2fGiB", metrics.AvgCpuPerNode, metrics.AvgMemPerNode)
}

// shouldScaleUp determines if scale-up conditions are met
func shouldScaleUp(metrics *ResourceMetrics, cfg *Config, idleRunnersCount, nascentNodesCount int) bool {
	isCpuUtilizationTooHigh := false
	if metrics.TotalCPUCapacity > 0 {
		isCpuUtilizationTooHigh = (metrics.TotalAllocatedCPU/metrics.TotalCPUCapacity)*100 > float32(cfg.MaxResourceUtilizationPercent)
	}
	isMemUtilizationTooHigh := false
	if metrics.TotalMemoryGiBCapacity > 0 {
		isMemUtilizationTooHigh = (metrics.TotalAllocatedMemoryGiB/metrics.TotalMemoryGiBCapacity)*100 > float32(cfg.MaxResourceUtilizationPercent)
	}
	isUtilizationTooHigh := isCpuUtilizationTooHigh || isMemUtilizationTooHigh

	totalIdleRunnersIncludingNascent := idleRunnersCount + nascentNodesCount
	isIdleRunnerBufferTooLow := totalIdleRunnersIncludingNascent < cfg.MinIdleRunners

	isCpuIdleTooLow := metrics.TotalAvailableCPU < float32(cfg.MinIdleCpu)
	isMemIdleTooLow := metrics.TotalAvailableMemoryGiB < float32(cfg.MinIdleMemory)

	return isUtilizationTooHigh || isIdleRunnerBufferTooLow || isCpuIdleTooLow || isMemIdleTooLow
}

// handleScaleUp handles scale-up logic and returns true if scale-up was triggered
func handleScaleUp(clientset *kubernetes.Clientset, cfg *Config, state *ClusterState, metrics *ResourceMetrics) bool {
	isCpuUtilizationTooHigh := false
	if metrics.TotalCPUCapacity > 0 {
		isCpuUtilizationTooHigh = (metrics.TotalAllocatedCPU/metrics.TotalCPUCapacity)*100 > float32(cfg.MaxResourceUtilizationPercent)
	}
	isMemUtilizationTooHigh := false
	if metrics.TotalMemoryGiBCapacity > 0 {
		isMemUtilizationTooHigh = (metrics.TotalAllocatedMemoryGiB/metrics.TotalMemoryGiBCapacity)*100 > float32(cfg.MaxResourceUtilizationPercent)
	}
	isUtilizationTooHigh := isCpuUtilizationTooHigh || isMemUtilizationTooHigh

	totalIdleRunnersIncludingNascent := len(state.IdleRunners) + len(state.NascentNodes)
	isIdleRunnerBufferTooLow := totalIdleRunnersIncludingNascent < cfg.MinIdleRunners
	isCpuIdleTooLow := metrics.TotalAvailableCPU < float32(cfg.MinIdleCpu)
	isMemIdleTooLow := metrics.TotalAvailableMemoryGiB < float32(cfg.MinIdleMemory)

	log.Printf("Scale-up conditions met: UtilizationTooHigh: %t (CPU: %.2f%%, Mem: %.2f%%), IdleBufferTooLow: %t (%d < %d), CpuIdleTooLow: %t (%.2f < %d), MemIdleTooLow: %t (%.2f < %d)",
		isUtilizationTooHigh, (metrics.TotalAllocatedCPU/metrics.TotalCPUCapacity)*100, (metrics.TotalAllocatedMemoryGiB/metrics.TotalMemoryGiBCapacity)*100,
		isIdleRunnerBufferTooLow, totalIdleRunnersIncludingNascent, cfg.MinIdleRunners,
		isCpuIdleTooLow, metrics.TotalAvailableCPU, cfg.MinIdleCpu,
		isMemIdleTooLow, metrics.TotalAvailableMemoryGiB, cfg.MinIdleMemory)

	var nodesNeededFromDeficit int

	if isCpuIdleTooLow && metrics.AvgCpuPerNode > 0 {
		needed := int(math.Ceil(float64(float32(cfg.MinIdleCpu)-metrics.TotalAvailableCPU) / float64(metrics.AvgCpuPerNode)))
		nodesNeededFromDeficit = max(nodesNeededFromDeficit, needed)
	}
	if isMemIdleTooLow && metrics.AvgMemPerNode > 0 {
		needed := int(math.Ceil(float64(float32(cfg.MinIdleMemory)-metrics.TotalAvailableMemoryGiB) / float64(metrics.AvgMemPerNode)))
		nodesNeededFromDeficit = max(nodesNeededFromDeficit, needed)
	}
	if isIdleRunnerBufferTooLow {
		needed := cfg.MinIdleRunners - totalIdleRunnersIncludingNascent
		nodesNeededFromDeficit = max(nodesNeededFromDeficit, needed)
	}

	if isUtilizationTooHigh && nodesNeededFromDeficit == 0 {
		nodesNeededFromDeficit = 1
	}

	nodesToCreate := nodesNeededFromDeficit - len(state.PendingPlaceholders)

	if nodesToCreate > 0 {
		log.Printf("Triggering scale-up: Creating %d placeholder pods. (Calculated need: %d, In-flight: %d)",
			nodesToCreate, nodesNeededFromDeficit, len(state.PendingPlaceholders))
		for i := 0; i < nodesToCreate; i++ {
			if _, err := createPlaceholderPod(clientset, cfg.ProviderNamespace, PlaceholderPodLabel); err != nil {
				log.Printf("Error creating placeholder pod for scale-up: %v", err)
			}
		}
		return true
	}

	log.Printf("Scale-up conditions met, but no new pods to create (already %d in-flight). Waiting for nodes to provision.", len(state.PendingPlaceholders))
	return false
}

// handleScaleDown handles scale-down logic
func handleScaleDown(clientset *kubernetes.Clientset, cfg *Config, state *ClusterState, metrics *ResourceMetrics, needsScaleUp bool) {
	// First, handle pending placeholders based on resource conditions
	// If we don't need to scale up and there are pending placeholders, delete them
	// to prevent unnecessary node provisioning
	if !needsScaleUp && len(state.PendingPlaceholders) > 0 {
		log.Printf("No scale-up needed but found %d pending placeholder pods. Deleting them to prevent unnecessary node provisioning.", len(state.PendingPlaceholders))
		for _, pendingPod := range state.PendingPlaceholders {
			log.Printf("Deleting pending placeholder pod %s since scale-up is not needed.", pendingPod.Name)
			err := clientset.CoreV1().Pods(cfg.ProviderNamespace).Delete(context.Background(), pendingPod.Name, metav1.DeleteOptions{})
			if err != nil {
				log.Printf("Error deleting pending placeholder pod %s: %v", pendingPod.Name, err)
			}
		}
	}

	if len(state.DeletableRunners) == 0 {
		log.Println("No deletable runners found for scale-down.")
		return
	}

	var placeholdersToDeleteInBatch []*corev1.Pod
	log.Printf("Considering scale-down for %d deletable runners.", len(state.DeletableRunners))

	for _, runnerToScaleDown := range state.DeletableRunners {
		domainToScaleDown := runnerToScaleDown.GetDomain()
		if domainToScaleDown == "" {
			log.Printf("Warning: Deletable runner %s has no domain, skipping.", runnerToScaleDown.GetName())
			continue
		}

		// Find the K8s Node object for this runner by matching domain (IP)
		var k8sNode *corev1.Node
		var nodeName string
		if node, found := state.NodeByIP[domainToScaleDown]; found {
			k8sNode = node
			nodeName = node.Name
		} else {
			log.Printf("Warning: Could not find K8s Node for deletable runner with domain %s. Skipping.", domainToScaleDown)
			continue
		}

		nodeCpuCapacity, nodeMemCapacity, err := getNodeAllocatableResources(k8sNode)
		if err != nil {
			log.Printf("Warning: Could not get allocatable resources for K8s Node %s: %v. Skipping scale-down check.", nodeName, err)
			continue
		}

		// Scale-down safety check
		hypotheticalAvailableCpu := metrics.TotalAvailableCPU - nodeCpuCapacity
		hypotheticalAvailableMemoryGiB := metrics.TotalAvailableMemoryGiB - nodeMemCapacity

		isSafeToDelete := true
		if hypotheticalAvailableCpu < float32(cfg.MinIdleCpu) {
			log.Printf("Scale-down of %s (%s) would violate MIN_IDLE_CPU (would be %.2f, min is %d). Skipping.", nodeName, domainToScaleDown, hypotheticalAvailableCpu, cfg.MinIdleCpu)
			isSafeToDelete = false
		}
		if hypotheticalAvailableMemoryGiB < float32(cfg.MinIdleMemory) {
			log.Printf("Scale-down of %s (%s) would violate MIN_IDLE_MEMORY (would be %.2f, min is %d). Skipping.", nodeName, domainToScaleDown, hypotheticalAvailableMemoryGiB, cfg.MinIdleMemory)
			isSafeToDelete = false
		}

		if !isSafeToDelete {
			continue
		}

		// Find the corresponding placeholder pod to delete
		var placeholderFound *corev1.Pod
		for _, pod := range state.ScheduledPlaceholders {
			if pod.Spec.NodeName == nodeName {
				placeholderFound = pod
				break
			}
		}

		if placeholderFound != nil {
			placeholdersToDeleteInBatch = append(placeholdersToDeleteInBatch, placeholderFound)
			log.Printf("Identified placeholder pod %s on node %s for deletion (runner domain %s). Safe to delete.", placeholderFound.Name, nodeName, domainToScaleDown)
		} else {
			log.Printf("Warning: Could not find a scheduled placeholder pod on node %s for deletable runner with domain %s. It might have been manually removed or never properly created. Skipping deletion of Daytona runner.", nodeName, domainToScaleDown)
		}
	}

	// Execute batch deletion
	for _, pod := range placeholdersToDeleteInBatch {
		log.Printf("Deleting placeholder pod %s for scale-down.", pod.Name)
		err := clientset.CoreV1().Pods(cfg.ProviderNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Printf("Error deleting placeholder pod %s: %v", pod.Name, err)
		}
	}
	if len(placeholdersToDeleteInBatch) > 0 {
		log.Printf("Successfully initiated deletion of %d placeholder pods for scale-down.", len(placeholdersToDeleteInBatch))
	} else {
		log.Println("No safe-to-delete placeholder pods identified for scale-down in this cycle.")
	}
}

// getNodeAllocatableResources queries a Kubernetes Node object and returns its allocatable CPU (in cores) and Memory (in GiB).
func getNodeAllocatableResources(node *corev1.Node) (cpuCores float32, memoryGiB float32, err error) {
	// Blank import to force usage recognition by compiler/linter
	_ = resource.Quantity{}

	cpuAllocatable := node.Status.Allocatable[corev1.ResourceCPU]
	memoryAllocatable := node.Status.Allocatable[corev1.ResourceMemory]

	// Convert CPU to cores (float32)
	cpuCores = float32(cpuAllocatable.MilliValue()) / 1000

	// Convert Memory to GiB (float32)
	memoryBytes := memoryAllocatable.Value()
	memoryGiB = float32(memoryBytes) / (1024 * 1024 * 1024)

	return cpuCores, memoryGiB, nil
}

// extractNodeIPs extracts IP addresses directly from a node object
func extractNodeIPs(node *corev1.Node) []string {
	var ips []string
	for _, addr := range node.Status.Addresses {
		ips = append(ips, addr.Address)
	}
	return ips
}

// createPlaceholderPod creates a Kubernetes Pod that acts as a placeholder to trigger cluster autoscaling.
func createPlaceholderPod(clientset *kubernetes.Clientset, namespace, appName string) (*corev1.Pod, error) {
	podName := fmt.Sprintf("%s-%s", appName, strings.ToLower(generateRandomString(8))) // Unique name
	log.Printf("Creating placeholder pod %s in namespace %s", podName, namespace)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": appName, // Label to easily find these pods later
			},
		},
		Spec: corev1.PodSpec{
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "app",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{appName},
									},
								},
							},
							TopologyKey: "kubernetes.io/hostname",
						},
					},
				},
			},
			NodeSelector: map[string]string{
				NodeSelectorKey: "true",
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      TaintKey,
					Operator: corev1.TolerationOpEqual,
					Value:    "true",
					Effect:   corev1.TaintEffectNoExecute,
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "pause",
					Image: "rancher/pause:3.6", // A very small, stable image
				},
			},
			RestartPolicy: corev1.RestartPolicyNever, // Don't restart if it completes
		},
	}

	createdPod, err := clientset.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create placeholder pod %s: %w", podName, err)
	}

	log.Printf("Successfully created placeholder pod %s", createdPod.Name)
	return createdPod, nil
}

// generateRandomString generates a random string of fixed length.
func generateRandomString(length int) string {
	var result strings.Builder
	charset := "abcdefghijklmnopqrstuvwxyz0123456789"
	for i := 0; i < length; i++ {
		result.WriteByte(charset[time.Now().UnixNano()%int64(len(charset))])
	}
	return result.String()
}

// max returns the larger of x or y (for integers).
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
