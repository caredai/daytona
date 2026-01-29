"use strict";
/*
 * Copyright 2025 Daytona Platforms Inc.
 * SPDX-License-Identifier: Apache-2.0
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.Daytona = exports.CodeLanguage = void 0;
const tslib_1 = require("tslib");
const api_client_1 = require("@daytonaio/api-client");
const axios_1 = tslib_1.__importStar(require("axios"));
const SandboxPythonCodeToolbox_1 = require("./code-toolbox/SandboxPythonCodeToolbox");
const SandboxTsCodeToolbox_1 = require("./code-toolbox/SandboxTsCodeToolbox");
const SandboxJsCodeToolbox_1 = require("./code-toolbox/SandboxJsCodeToolbox");
const DaytonaError_1 = require("./errors/DaytonaError");
const Image_1 = require("./Image");
const Sandbox_1 = require("./Sandbox");
const Snapshot_1 = require("./Snapshot");
const Volume_1 = require("./Volume");
const packageJson = tslib_1.__importStar(require("../package.json"));
const Stream_1 = require("./utils/Stream");
const Runtime_1 = require("./utils/Runtime");
/**
 * Supported programming languages for code execution
 *
 * Python is used as the default sandbox language when no language is explicitly specified.
 */
var CodeLanguage;
(function (CodeLanguage) {
    CodeLanguage["PYTHON"] = "python";
    CodeLanguage["TYPESCRIPT"] = "typescript";
    CodeLanguage["JAVASCRIPT"] = "javascript";
})(CodeLanguage || (exports.CodeLanguage = CodeLanguage = {}));
/**
 * Main class for interacting with the Daytona API.
 * Provides methods for creating, managing, and interacting with Daytona Sandboxes.
 * Can be initialized either with explicit configuration or using environment variables.
 *
 * @property {VolumeService} volume - Service for managing Daytona Volumes
 * @property {SnapshotService} snapshot - Service for managing Daytona Snapshots
 *
 * @example
 * // Using environment variables
 * // Uses DAYTONA_API_KEY, DAYTONA_API_URL, DAYTONA_TARGET
 * const daytona = new Daytona();
 * const sandbox = await daytona.create();
 *
 * @example
 * // Using explicit configuration
 * const config: DaytonaConfig = {
 *     apiKey: "your-api-key",
 *     apiUrl: "https://your-api.com",
 *     target: "us"
 * };
 * const daytona = new Daytona(config);
 *
 * @class
 */
class Daytona {
    clientConfig;
    sandboxApi;
    objectStorageApi;
    configApi;
    target;
    apiKey;
    jwtToken;
    organizationId;
    apiUrl;
    // Toolbox proxy cache per region
    toolboxProxyCache = new Map();
    volume;
    snapshot;
    /**
     * Creates a new Daytona client instance.
     *
     * @param {DaytonaConfig} [config] - Configuration options
     * @throws {DaytonaError} - `DaytonaError` - When API key is missing
     */
    constructor(config) {
        let apiUrl;
        if (config) {
            this.apiKey = !config?.apiKey && config?.jwtToken ? undefined : config?.apiKey;
            this.jwtToken = config?.jwtToken;
            this.organizationId = config?.organizationId;
            apiUrl = config?.apiUrl || config?.serverUrl;
            this.target = config?.target;
        }
        if ((!config ||
            (!(this.apiKey && apiUrl && this.target) &&
                !(this.jwtToken && this.organizationId && apiUrl && this.target))) &&
            Runtime_1.RUNTIME !== Runtime_1.Runtime.BROWSER) {
            if (Runtime_1.RUNTIME === Runtime_1.Runtime.NODE && typeof require !== 'undefined') {
                const dotenv = require('dotenv');
                dotenv.config({ quiet: true });
                dotenv.config({ path: '.env.local', override: true, quiet: true });
            }
            this.apiKey = this.apiKey || (this.jwtToken ? undefined : (0, Runtime_1.getEnvVar)('DAYTONA_API_KEY'));
            this.jwtToken = this.jwtToken || (0, Runtime_1.getEnvVar)('DAYTONA_JWT_TOKEN');
            this.organizationId = this.organizationId || (0, Runtime_1.getEnvVar)('DAYTONA_ORGANIZATION_ID');
            apiUrl = apiUrl || (0, Runtime_1.getEnvVar)('DAYTONA_API_URL') || (0, Runtime_1.getEnvVar)('DAYTONA_SERVER_URL');
            this.target = this.target || (0, Runtime_1.getEnvVar)('DAYTONA_TARGET');
            if ((0, Runtime_1.getEnvVar)('DAYTONA_SERVER_URL') && !(0, Runtime_1.getEnvVar)('DAYTONA_API_URL')) {
                console.warn('[Deprecation Warning] Environment variable `DAYTONA_SERVER_URL` is deprecated and will be removed in future versions. Use `DAYTONA_API_URL` instead.');
            }
        }
        this.apiUrl = apiUrl || 'https://app.daytona.io/api';
        const orgHeader = {};
        if (!this.apiKey) {
            if (!this.organizationId) {
                throw new DaytonaError_1.DaytonaError('Organization ID is required when using JWT token');
            }
            orgHeader['X-Daytona-Organization-ID'] = this.organizationId;
        }
        const configuration = new api_client_1.Configuration({
            basePath: this.apiUrl,
            baseOptions: {
                headers: {
                    Authorization: `Bearer ${this.apiKey || this.jwtToken}`,
                    'X-Daytona-Source': 'typescript-sdk',
                    'X-Daytona-SDK-Version': packageJson.version,
                    ...orgHeader,
                },
            },
        });
        const axiosInstance = this.createAxiosInstance();
        this.sandboxApi = new api_client_1.SandboxApi(configuration, '', axiosInstance);
        this.objectStorageApi = new api_client_1.ObjectStorageApi(configuration, '', axiosInstance);
        this.configApi = new api_client_1.ConfigApi(configuration, '', axiosInstance);
        this.volume = new Volume_1.VolumeService(new api_client_1.VolumesApi(configuration, '', axiosInstance));
        this.snapshot = new Snapshot_1.SnapshotService(configuration, new api_client_1.SnapshotsApi(configuration, '', axiosInstance), this.objectStorageApi, this.target);
        this.clientConfig = configuration;
    }
    async create(params, options = { timeout: 60 }) {
        const startTime = Date.now();
        options = typeof options === 'number' ? { timeout: options } : { ...options };
        if (options.timeout == undefined || options.timeout == null) {
            options.timeout = 60;
        }
        if (params == null) {
            params = { language: 'python' };
        }
        const labels = params.labels || {};
        if (params.language) {
            labels['code-toolbox-language'] = params.language;
        }
        if (options.timeout < 0) {
            throw new DaytonaError_1.DaytonaError('Timeout must be a non-negative number');
        }
        if (params.autoStopInterval !== undefined &&
            (!Number.isInteger(params.autoStopInterval) || params.autoStopInterval < 0)) {
            throw new DaytonaError_1.DaytonaError('autoStopInterval must be a non-negative integer');
        }
        if (params.ephemeral) {
            if (params.autoDeleteInterval !== undefined && params.autoDeleteInterval !== 0) {
                console.warn("'ephemeral' and 'autoDeleteInterval' cannot be used together. If ephemeral is true, autoDeleteInterval will be ignored and set to 0.");
            }
            params.autoDeleteInterval = 0;
        }
        if (params.autoArchiveInterval !== undefined &&
            (!Number.isInteger(params.autoArchiveInterval) || params.autoArchiveInterval < 0)) {
            throw new DaytonaError_1.DaytonaError('autoArchiveInterval must be a non-negative integer');
        }
        const codeToolbox = this.getCodeToolbox(params.language);
        try {
            let buildInfo;
            let snapshot;
            let resources;
            if ('snapshot' in params) {
                snapshot = params.snapshot;
            }
            if ('image' in params) {
                if (typeof params.image === 'string') {
                    buildInfo = {
                        dockerfileContent: Image_1.Image.base(params.image).dockerfile,
                    };
                }
                else if (params.image instanceof Image_1.Image) {
                    const contextHashes = await Snapshot_1.SnapshotService.processImageContext(this.objectStorageApi, params.image);
                    buildInfo = {
                        contextHashes,
                        dockerfileContent: params.image.dockerfile,
                    };
                }
            }
            if ('resources' in params) {
                resources = params.resources;
            }
            const response = await this.sandboxApi.createSandbox({
                name: params.name,
                snapshot: snapshot,
                buildInfo,
                user: params.user,
                env: params.envVars || {},
                labels: labels,
                public: params.public,
                target: params.target,
                cpu: resources?.cpu,
                gpu: resources?.gpu,
                memory: resources?.memory,
                disk: resources?.disk,
                autoStopInterval: params.autoStopInterval,
                autoArchiveInterval: params.autoArchiveInterval,
                autoDeleteInterval: params.autoDeleteInterval,
                volumes: params.volumes,
                networkBlockAll: params.networkBlockAll,
                networkAllowList: params.networkAllowList,
            }, undefined, {
                timeout: options.timeout * 1000,
            });
            let sandboxInstance = response.data;
            if (sandboxInstance.state === api_client_1.SandboxState.PENDING_BUILD && options.onSnapshotCreateLogs) {
                const terminalStates = [
                    api_client_1.SandboxState.STARTED,
                    api_client_1.SandboxState.STARTING,
                    api_client_1.SandboxState.ERROR,
                    api_client_1.SandboxState.BUILD_FAILED,
                ];
                while (sandboxInstance.state === api_client_1.SandboxState.PENDING_BUILD) {
                    if (options.timeout) {
                        const elapsed = (Date.now() - startTime) / 1000;
                        if (elapsed > options.timeout) {
                            throw new DaytonaError_1.DaytonaError(`Sandbox build has been pending for more than ${options.timeout} seconds. Please check the sandbox state again later.`);
                        }
                    }
                    await new Promise((resolve) => setTimeout(resolve, 1000));
                    sandboxInstance = (await this.sandboxApi.getSandbox(sandboxInstance.id)).data;
                }
                const response = await this.sandboxApi.getBuildLogsUrl(sandboxInstance.id);
                await (0, Stream_1.processStreamingResponse)(() => fetch(response.data.url + '?follow=true', {
                    method: 'GET',
                    headers: this.clientConfig.baseOptions.headers,
                }), (chunk) => options.onSnapshotCreateLogs?.(chunk.trimEnd()), async () => {
                    sandboxInstance = (await this.sandboxApi.getSandbox(sandboxInstance.id)).data;
                    return sandboxInstance.state !== undefined && terminalStates.includes(sandboxInstance.state);
                });
            }
            const sandbox = new Sandbox_1.Sandbox(sandboxInstance, new api_client_1.Configuration(structuredClone(this.clientConfig)), this.createAxiosInstance(), this.sandboxApi, codeToolbox, this.getProxyToolboxUrl.bind(this));
            if (sandbox.state !== 'started') {
                const timeElapsed = Date.now() - startTime;
                await sandbox.waitUntilStarted(options.timeout ? Math.max(0.001, options.timeout - timeElapsed / 1000) : options.timeout);
            }
            return sandbox;
        }
        catch (error) {
            if (error instanceof DaytonaError_1.DaytonaError && error.message.includes('Operation timed out')) {
                const errMsg = `Failed to create and start sandbox within ${options.timeout} seconds. Operation timed out.`;
                throw new DaytonaError_1.DaytonaError(errMsg);
            }
            throw error;
        }
    }
    /**
     * Gets a Sandbox by its ID or name.
     *
     * @param {string} sandboxIdOrName - The ID or name of the Sandbox to retrieve
     * @returns {Promise<Sandbox>} The Sandbox
     *
     * @example
     * const sandbox = await daytona.get('my-sandbox-id-or-name');
     * console.log(`Sandbox state: ${sandbox.state}`);
     */
    async get(sandboxIdOrName) {
        const response = await this.sandboxApi.getSandbox(sandboxIdOrName);
        const sandboxInstance = response.data;
        const language = sandboxInstance.labels && sandboxInstance.labels['code-toolbox-language'];
        const codeToolbox = this.getCodeToolbox(language);
        return new Sandbox_1.Sandbox(sandboxInstance, structuredClone(this.clientConfig), this.createAxiosInstance(), this.sandboxApi, codeToolbox, this.getProxyToolboxUrl.bind(this));
    }
    /**
     * Finds a Sandbox by its ID or name or labels.
     *
     * @param {SandboxFilter} filter - Filter for Sandboxes
     * @returns {Promise<Sandbox>} First Sandbox that matches the ID or name or labels.
     *
     * @example
     * const sandbox = await daytona.findOne({ labels: { 'my-label': 'my-value' } });
     * console.log(`Sandbox ID: ${sandbox.id}, State: ${sandbox.state}`);
     */
    async findOne(filter) {
        if (filter.idOrName) {
            return this.get(filter.idOrName);
        }
        const result = await this.list(filter.labels, 1, 1);
        if (result.items.length === 0) {
            const errMsg = `No sandbox found with labels ${JSON.stringify(filter.labels)}`;
            throw new DaytonaError_1.DaytonaError(errMsg);
        }
        return result.items[0];
    }
    /**
     * Returns paginated list of Sandboxes filtered by labels.
     *
     * @param {Record<string, string>} [labels] - Labels to filter Sandboxes
     * @param {number} [page] - Page number for pagination (starting from 1)
     * @param {number} [limit] - Maximum number of items per page
     * @returns {Promise<PaginatedSandboxes>} Paginated list of Sandboxes that match the labels.
     *
     * @example
     * const result = await daytona.list({ 'my-label': 'my-value' }, 2, 10);
     * for (const sandbox of result.items) {
     *     console.log(`${sandbox.id}: ${sandbox.state}`);
     * }
     */
    async list(labels, page, limit) {
        const response = await this.sandboxApi.listSandboxesPaginated(undefined, page, limit, undefined, undefined, labels ? JSON.stringify(labels) : undefined);
        return {
            items: response.data.items.map((sandbox) => {
                const language = sandbox.labels?.['code-toolbox-language'];
                return new Sandbox_1.Sandbox(sandbox, structuredClone(this.clientConfig), this.createAxiosInstance(), this.sandboxApi, this.getCodeToolbox(language), this.getProxyToolboxUrl.bind(this));
            }),
            total: response.data.total,
            page: response.data.page,
            totalPages: response.data.totalPages,
        };
    }
    /**
     * Starts a Sandbox and waits for it to be ready.
     *
     * @param {Sandbox} sandbox - The Sandbox to start
     * @param {number} [timeout] - Optional timeout in seconds (0 means no timeout)
     * @returns {Promise<void>}
     *
     * @example
     * const sandbox = await daytona.get('my-sandbox-id');
     * // Wait up to 60 seconds for the sandbox to start
     * await daytona.start(sandbox, 60);
     */
    async start(sandbox, timeout) {
        await sandbox.start(timeout);
    }
    /**
     * Stops a Sandbox.
     *
     * @param {Sandbox} sandbox - The Sandbox to stop
     * @returns {Promise<void>}
     *
     * @example
     * const sandbox = await daytona.get('my-sandbox-id');
     * await daytona.stop(sandbox);
     */
    async stop(sandbox) {
        await sandbox.stop();
    }
    /**
     * Deletes a Sandbox.
     *
     * @param {Sandbox} sandbox - The Sandbox to delete
     * @param {number} timeout - Timeout in seconds (0 means no timeout, default is 60)
     * @returns {Promise<void>}
     *
     * @example
     * const sandbox = await daytona.get('my-sandbox-id');
     * await daytona.delete(sandbox);
     */
    async delete(sandbox, timeout = 60) {
        await sandbox.delete(timeout);
    }
    /**
     * Gets the appropriate code toolbox based on language.
     *
     * @private
     * @param {CodeLanguage} [language] - Programming language for the toolbox
     * @returns {SandboxCodeToolbox} The appropriate code toolbox instance
     * @throws {DaytonaError} - `DaytonaError` - When an unsupported language is specified
     */
    getCodeToolbox(language) {
        switch (language) {
            case CodeLanguage.JAVASCRIPT:
                return new SandboxJsCodeToolbox_1.SandboxJsCodeToolbox();
            case CodeLanguage.TYPESCRIPT:
                return new SandboxTsCodeToolbox_1.SandboxTsCodeToolbox();
            case CodeLanguage.PYTHON:
            case undefined:
                return new SandboxPythonCodeToolbox_1.SandboxPythonCodeToolbox();
            default: {
                const errMsg = `Unsupported language: ${language}, supported languages: ${Object.values(CodeLanguage).join(', ')}`;
                throw new DaytonaError_1.DaytonaError(errMsg);
            }
        }
    }
    createAxiosInstance() {
        const axiosInstance = axios_1.default.create({
            timeout: 24 * 60 * 60 * 1000, // 24 hours
        });
        axiosInstance.interceptors.response.use((response) => {
            return response;
        }, (error) => {
            let errorMessage;
            if (error instanceof axios_1.AxiosError && error.message.includes('timeout of')) {
                errorMessage = 'Operation timed out';
            }
            else {
                errorMessage = error.response?.data?.message || error.response?.data || error.message || String(error);
            }
            if (typeof errorMessage === 'object') {
                try {
                    errorMessage = JSON.stringify(errorMessage);
                }
                catch {
                    errorMessage = String(errorMessage);
                }
            }
            const statusCode = error.response?.status;
            const headers = error.response?.headers;
            switch (statusCode) {
                case 404:
                    throw new DaytonaError_1.DaytonaNotFoundError(errorMessage, statusCode, headers);
                case 429:
                    throw new DaytonaError_1.DaytonaRateLimitError(errorMessage, statusCode, headers);
                default:
                    throw new DaytonaError_1.DaytonaError(errorMessage, statusCode, headers);
            }
        });
        return axiosInstance;
    }
    async getProxyToolboxUrl(sandboxId, regionId) {
        const cachedProxyToolboxUrl = this.toolboxProxyCache.get(regionId);
        if (cachedProxyToolboxUrl) {
            return await cachedProxyToolboxUrl;
        }
        const proxyToolboxUrlPromise = (async () => {
            const response = await this.sandboxApi.getToolboxProxyUrl(sandboxId);
            return response.data.url;
        })();
        this.toolboxProxyCache.set(regionId, proxyToolboxUrlPromise);
        return await proxyToolboxUrlPromise;
    }
}
exports.Daytona = Daytona;
//# sourceMappingURL=Daytona.js.map