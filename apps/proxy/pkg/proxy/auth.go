// Copyright 2025 Daytona Platforms Inc.
// SPDX-License-Identifier: AGPL-3.0

package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func (p *Proxy) Authenticate(ctx *gin.Context, sandboxIdOrSignedToken string, port float32) (sandboxId string, didRedirect bool, err error) {
	var authErrors []string

	// Try Authorization header with Bearer token
	authHeader := ctx.Request.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		bearerToken := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		startTime := time.Now()
		isValid, err := p.getSandboxBearerTokenValid(ctx, sandboxIdOrSignedToken, bearerToken)
		duration := time.Since(startTime)
		if err != nil {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("duration", duration).
				WithError(err).
				Error("Bearer token validation failed")
			authErrors = append(authErrors, fmt.Sprintf("Bearer token validation error: %v", err))
		} else if isValid != nil && *isValid {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("duration", duration).
				Info("Bearer token validation successful")
			return sandboxIdOrSignedToken, false, nil
		} else {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("duration", duration).
				Warn("Bearer token is invalid")
			authErrors = append(authErrors, "Bearer token is invalid")
		}
	}

	// Try auth key from header
	authKey := ctx.Request.Header.Get(SANDBOX_AUTH_KEY_HEADER)
	if authKey != "" {
		ctx.Request.Header.Del(SANDBOX_AUTH_KEY_HEADER)
		startTime := time.Now()
		isValid, err := p.getSandboxAuthKeyValid(ctx, sandboxIdOrSignedToken, authKey)
		duration := time.Since(startTime)
		if err != nil {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("authKey", authKey).
				WithField("duration", duration).
				WithError(err).
				Error("Auth key header validation failed")
			authErrors = append(authErrors, fmt.Sprintf("Auth key header validation error: %v", err))
		} else if isValid != nil && *isValid {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("duration", duration).
				Info("Auth key header validation successful")
			return sandboxIdOrSignedToken, false, nil
		} else {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("authKey", authKey).
				WithField("duration", duration).
				Warn("Auth key from header is invalid")
			authErrors = append(authErrors, "Auth key header is invalid")
		}
	}

	// Try auth key from query parameter
	queryAuthKey := ctx.Query(SANDBOX_AUTH_KEY_QUERY_PARAM)
	if queryAuthKey != "" {
		startTime := time.Now()
		isValid, err := p.getSandboxAuthKeyValid(ctx, sandboxIdOrSignedToken, queryAuthKey)
		duration := time.Since(startTime)
		if err != nil {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("queryAuthKey", queryAuthKey).
				WithField("duration", duration).
				WithError(err).
				Error("Auth key query param validation failed")
			authErrors = append(authErrors, fmt.Sprintf("Auth key query param validation error: %v", err))
		} else if isValid != nil && *isValid {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("duration", duration).
				Info("Auth key query param validation successful")
			// Remove the auth key from the query string
			newQuery := ctx.Request.URL.Query()
			newQuery.Del(SANDBOX_AUTH_KEY_QUERY_PARAM)
			ctx.Request.URL.RawQuery = newQuery.Encode()
			return sandboxIdOrSignedToken, false, nil
		} else {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("queryAuthKey", queryAuthKey).
				WithField("duration", duration).
				Warn("Auth key from query param is invalid")
			authErrors = append(authErrors, "Auth key query parameter is invalid")
		}
	}

	// Try cookie authentication
	cookieName := SANDBOX_AUTH_COOKIE_NAME + sandboxIdOrSignedToken
	cookieValue, err := ctx.Cookie(cookieName)
	if err == nil && cookieValue != "" {
		decodedValue := ""
		startTime := time.Now()
		err = p.secureCookie.Decode(cookieName, cookieValue, &decodedValue)
		duration := time.Since(startTime)
		if err != nil {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("cookieName", cookieName).
				WithField("duration", duration).
				WithError(err).
				Error("Cookie decoding failed")
			authErrors = append(authErrors, fmt.Sprintf("Cookie decoding error: %v", err))
		} else if decodedValue == sandboxIdOrSignedToken {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("duration", duration).
				Info("Cookie auth successful")
			return sandboxIdOrSignedToken, false, nil
		} else {
			log.WithField("sandboxId", sandboxIdOrSignedToken).
				WithField("cookieName", cookieName).
				WithField("decodedValue", decodedValue).
				WithField("duration", duration).
				Warn("Decoded cookie value does not match sandbox ID")
		}
	}

	cookieDomain := p.getCookieDomain(ctx.Request.Host)

	startTime := time.Now()
	sandboxId, err = p.getSandboxIdFromSignedPreviewUrlToken(ctx, sandboxIdOrSignedToken, port, cookieDomain)
	duration := time.Since(startTime)
	if err == nil {
		log.WithField("sandboxId", sandboxId).
			WithField("duration", duration).
			Info("Signed preview URL token validation successful")
		return sandboxId, false, nil
	} else {
		log.WithField("sandboxIdOrSignedToken", sandboxIdOrSignedToken).
			WithField("port", port).
			WithField("duration", duration).
			WithError(err).
			Error("Signed preview URL token validation failed")
		authErrors = append(authErrors, err.Error())
	}

	// All authentication methods failed, redirect to auth URL
	authUrl, err := p.getAuthUrl(ctx, sandboxIdOrSignedToken)
	if err != nil {
		return sandboxIdOrSignedToken, false, fmt.Errorf("failed to get auth URL: %w", err)
	}

	ctx.Redirect(http.StatusTemporaryRedirect, authUrl)

	// Return error with details about what failed
	var errorMsg string
	if len(authErrors) > 0 {
		errorMsg = fmt.Sprintf("authentication failed:\n%s", strings.Join(authErrors, "\n;\n"))
	} else {
		errorMsg = "missing authentication: provide a preview access token (via header, query parameter, or cookie) or use an API key or JWT"
	}

	return sandboxIdOrSignedToken, true, errors.New(errorMsg)
}

func (p *Proxy) getSandboxIdFromSignedPreviewUrlToken(ctx *gin.Context, sandboxIdOrSignedToken string, port float32, cookieDomain string) (string, error) {
	sandboxId, _, err := p.apiclient.PreviewAPI.GetSandboxIdFromSignedPreviewUrlToken(ctx.Request.Context(), sandboxIdOrSignedToken, port).Execute()
	if err != nil {
		return "", fmt.Errorf("failed to get sandbox ID: %w. Is the token expired?", err)
	}

	encoded, err := p.secureCookie.Encode(SANDBOX_AUTH_COOKIE_NAME+sandboxId, sandboxId)
	if err != nil {
		return "", fmt.Errorf("failed to encode cookie: %w", err)
	}

	ctx.SetCookie(SANDBOX_AUTH_COOKIE_NAME+sandboxId, encoded, 3600, "/", cookieDomain, p.config.EnableTLS, true)

	return sandboxId, nil
}
