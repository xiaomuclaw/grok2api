package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	accountdomain "github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/audit"
	"github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	modeldomain "github.com/chenyme/grok2api/backend/internal/domain/model"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/pkg/requestmeta"
)

const (
	imageRoutingMaxAttempts    = 5
	maxImageRoutingAttempts    = 20
	imageQuotaFallbackCooldown = 10 * time.Minute
	imageQuotaBlockJitter      = 2 * time.Minute
)

// imageQuotaRetryAfter blocks an exhausted image account until its rolling
// quota reset. The jitter avoids every account waking at the same second.
func imageQuotaRetryAfter(lease *accountLease, now time.Time, fallback time.Duration) time.Duration {
	if lease != nil && lease.routingCandidate != nil && lease.routingCandidate.QuotaWindow != nil {
		if resetAt := lease.routingCandidate.QuotaWindow.ResetAt; resetAt != nil {
			if remaining := resetAt.Sub(now); remaining > 0 {
				return remaining + imageQuotaBlockJitter
			}
		}
	}
	if fallback <= 0 {
		return imageQuotaFallbackCooldown
	}
	return fallback
}

// ImageGenerationInput 表示图片生成用例已经完成协议校验后的输入。
type ImageGenerationInput struct {
	RequestID      string
	ClientKey      clientkey.Key
	PublicModel    string
	Prompt         string
	Count          int
	Size           string
	AspectRatio    string
	Resolution     string
	Quality        string
	ResponseFormat string
	Streaming      bool
	PartialImages  int
	Method         string
	Path           string
	Headers        map[string][]string
}

// ImageEditInput 表示图片编辑用例已经完成协议校验后的输入。
type ImageEditInput struct {
	RequestID      string
	ClientKey      clientkey.Key
	PublicModel    string
	Prompt         string
	ImageURLs      []string
	Count          int
	Size           string
	AspectRatio    string
	Resolution     string
	Quality        string
	ResponseFormat string
	Streaming      bool
	PartialImages  int
	Method         string
	Path           string
	Headers        map[string][]string
}

type imageProviderSupport func(accountdomain.Provider) bool

type imageExecution func(context.Context, accountdomain.Provider, accountdomain.Credential, string) (*provider.Response, error)

type imageOutputPlan struct {
	upstreamResolution string
	targetMaxEdge      int
}

func planImageOutput(providerValue accountdomain.Provider, operation audit.Operation, requestedResolution string) imageOutputPlan {
	requestedResolution = strings.ToLower(strings.TrimSpace(requestedResolution))
	targetMaxEdge := 0
	switch requestedResolution {
	case "2k":
		targetMaxEdge = 2048
	case "4k":
		targetMaxEdge = 4096
	default:
		return imageOutputPlan{upstreamResolution: requestedResolution}
	}
	upstreamResolution := "2k"
	if providerValue == accountdomain.ProviderWeb && operation == audit.OperationImageEdit {
		upstreamResolution = "1k"
	}
	return imageOutputPlan{upstreamResolution: upstreamResolution, targetMaxEdge: targetMaxEdge}
}

// GenerateImage 选择支持图片生成的路由和账号，并返回可统一审计的上游响应。
func (s *Service) GenerateImage(ctx context.Context, input ImageGenerationInput) (*Result, error) {
	return s.executeImage(ctx, input.RequestID, input.ClientKey, input.PublicModel, audit.OperationImage, modeldomain.CapabilityImage, func(providerValue accountdomain.Provider) bool {
		_, ok := s.providers.ImageGeneration(providerValue)
		return ok
	}, func(executionCtx context.Context, providerValue accountdomain.Provider, credential accountdomain.Credential, upstream string) (*provider.Response, error) {
		adapter, ok := s.providers.ImageGeneration(providerValue)
		if !ok {
			return nil, ErrNoAvailableAccount
		}
		plan := planImageOutput(providerValue, audit.OperationImage, input.Resolution)
		return adapter.GenerateImage(executionCtx, provider.ImageGenerationRequest{
			Credential: credential, Model: upstream, Prompt: input.Prompt, Count: input.Count,
			Size: input.Size, AspectRatio: input.AspectRatio, Resolution: plan.upstreamResolution, Quality: input.Quality, TargetMaxEdge: plan.targetMaxEdge,
			ResponseFormat: input.ResponseFormat, Streaming: input.Streaming, PartialImages: input.PartialImages,
		})
	}, input.Streaming, input.Resolution, input.Quality, input.Count, 0, input.Method, input.Path, input.Headers)
}

// EditImage 选择支持图片编辑的路由和账号，并返回可统一审计的上游响应。
func (s *Service) EditImage(ctx context.Context, input ImageEditInput) (*Result, error) {
	return s.executeImage(ctx, input.RequestID, input.ClientKey, input.PublicModel, audit.OperationImageEdit, modeldomain.CapabilityImageEdit, func(providerValue accountdomain.Provider) bool {
		_, ok := s.providers.ImageEdit(providerValue)
		return ok
	}, func(executionCtx context.Context, providerValue accountdomain.Provider, credential accountdomain.Credential, upstream string) (*provider.Response, error) {
		adapter, ok := s.providers.ImageEdit(providerValue)
		if !ok {
			return nil, ErrNoAvailableAccount
		}
		plan := planImageOutput(providerValue, audit.OperationImageEdit, input.Resolution)
		return adapter.EditImage(executionCtx, provider.ImageEditRequest{
			Credential: credential, Model: upstream, Prompt: input.Prompt,
			ImageURLs: input.ImageURLs, Count: input.Count, Size: input.Size, AspectRatio: input.AspectRatio,
			Resolution: plan.upstreamResolution, Quality: input.Quality, TargetMaxEdge: plan.targetMaxEdge, ResponseFormat: input.ResponseFormat,
			Streaming: input.Streaming, PartialImages: input.PartialImages,
		})
	}, input.Streaming, input.Resolution, input.Quality, input.Count, len(input.ImageURLs), input.Method, input.Path, input.Headers)
}

func (s *Service) executeImage(
	ctx context.Context,
	requestID string,
	key clientkey.Key,
	publicModel string,
	operation audit.Operation,
	capability modeldomain.Capability,
	supports imageProviderSupport,
	execute imageExecution,
	streaming bool,
	resolution string,
	quality string,
	requestedCount int,
	inputImageCount int,
	method string,
	path string,
	headers map[string][]string,
) (*Result, error) {
	ctx, egressTrace := infraegress.WithTrace(ctx)
	startedAt := time.Now()
	eventID := newAuditEventID()
	routes, err := s.models.GetByPublicIDCandidates(ctx, publicModel)
	if err != nil {
		return nil, ErrModelNotFound
	}
	route, preselectedSession, err := s.selectSchedulableMediaRoute(ctx, routes, key, capability, true, supports)
	if err != nil {
		// Preserve the established failure-audit path when every eligible target
		// is currently unschedulable. The request loop will reproduce the
		// selection error for the representative route after auditBase exists.
		route, err = s.selectMediaRoute(routes, key, capability, supports)
		if err != nil {
			return nil, err
		}
		preselectedSession = nil
	}
	outputPlan := planImageOutput(route.Provider, operation, resolution)
	if outputPlan.targetMaxEdge > 0 && streaming {
		return nil, ErrImageUpscaleStreaming
	}
	if outputPlan.targetMaxEdge > 0 && !s.imageUpscaleEnabled.Load() {
		return nil, ErrImageUpscaleDisabled
	}
	externalModel := modeldomain.ExternalPublicID(route.Provider, route.PublicID)
	auditBase := audit.Record{
		EventID: eventID, RequestID: requestID, ClientKeyID: key.ID, ClientKeyName: key.Name,
		ClientIP:     requestmeta.ClientIP(ctx),
		ModelRouteID: route.ID, ModelPublicID: externalModel, ModelUpstreamModel: modeldomain.DisplayUpstreamModel(route.Provider, route.UpstreamModel),
		Provider: string(route.Provider), Operation: operation, UsageSource: audit.UsageSourceNone, Streaming: streaming,
		RequestMethod: method, RequestPath: path, RequestHeaders: headers,
	}
	if operation == audit.OperationImageEdit {
		auditBase.MediaInputImages = int64(max(0, inputImageCount))
	}
	if err := s.checkLedgerReady(); err != nil {
		return nil, err
	}
	attempts := make([]audit.Attempt, 0, imageRoutingMaxAttempts)
	writeFailureAudit := func(statusCode int, errorCode string, credential *accountdomain.Credential) {
		record := auditBase
		record.StatusCode = statusCode
		record.ErrorCode = errorCode
		record.AttemptCount = len(attempts)
		record.Attempts = append([]audit.Attempt(nil), attempts...)
		record.DurationMS = time.Since(startedAt).Milliseconds()
		record.CreatedAt = time.Now().UTC()
		if credential != nil {
			accountID := credential.ID
			record.AccountID = &accountID
			record.AccountName = credential.Name
		}
		applyAuditEgress(&record, egressTrace, route.Provider)
		persistCtx, cancel := context.WithTimeout(context.Background(), finalizationTimeout)
		defer cancel()
		if auditErr := s.audits.Create(persistCtx, record); auditErr != nil {
			s.logger.Error("request_usage_write_failed", "event_id", record.EventID, "request_id", requestID, "error", auditErr)
		}
	}
	recordAttempt := func(credential accountdomain.Credential, source audit.AttemptSource, stage string, started time.Time, status *int, err error) {
		accountID := credential.ID
		item := audit.Attempt{
			Number:             len(attempts) + 1,
			Source:             source,
			Stage:              stage,
			AccountID:          &accountID,
			AccountName:        credential.Name,
			StartedAt:          started,
			DurationMS:         time.Since(started).Milliseconds(),
			UpstreamStatusCode: status,
		}
		if err != nil {
			item.TransportError = err.Error()
		}
		attempts = append(attempts, item)
	}

	pricingModel := s.providers.PricingModel(route.Provider, route.UpstreamModel)
	pricingResolution, pricingQuality := outputPlan.upstreamResolution, quality
	if route.Provider == accountdomain.ProviderWeb && operation == audit.OperationImage {
		// Grok Web Imagine selects the product through the catalog model and
		// only forwards aspect_ratio/n. Do not reserve or record a price tier
		// derived from Console-only resolution/quality compatibility fields.
		pricingResolution, pricingQuality = "", ""
	}
	var reservation audit.PricingResult
	var priced bool
	switch operation {
	case audit.OperationImage:
		reservation, priced = audit.EstimateOfficialImageCost(pricingModel, pricingResolution, pricingQuality, requestedCount)
	case audit.OperationImageEdit:
		reservation, priced = audit.EstimateOfficialImageEditCost(pricingModel, pricingResolution, pricingQuality, requestedCount, inputImageCount)
	}
	reserved := false
	if priced {
		reserved, err = s.clientKeys.ReserveBilling(ctx, key, eventID, reservation.CostInUSDTicks, mediaBillingReservationTTL)
		if err != nil {
			return nil, err
		}
	}
	finalizationOwnsReservation := false
	defer func() {
		if reserved && !finalizationOwnsReservation {
			s.cancelBillingReservation(eventID)
		}
	}()
	quotaMode := s.providers.QuotaMode(route.Provider, route.UpstreamModel)
	quotaRefreshGroup := s.providers.QuotaRefreshGroup(route.Provider, route.UpstreamModel)
	imageAttempts := int(s.imageMaxAttempts.Load())
	if imageAttempts <= 0 {
		imageAttempts = imageRoutingMaxAttempts
	}
	if imageAttempts > maxImageRoutingAttempts {
		imageAttempts = maxImageRoutingAttempts
	}
	if configured := int(s.maxAttempts.Load()); configured > 0 && configured < imageAttempts {
		imageAttempts = configured
	}
	attemptPolicy := newRoutingAttemptPolicy(imageAttempts)
	excluded := make(map[uint64]bool)
	selection := preselectedSession
	var lease *accountLease
	var credential accountdomain.Credential
	var response *provider.Response
	var lastCredentialFailure *accountdomain.Credential
	var lastCredentialError error
	var quotaExhausted bool
	var earliestQuotaRetry time.Duration
	for attempt := 0; attemptPolicy.allows(attempt); attempt++ {
		attemptStarted := time.Now().UTC()
		if selection == nil {
			selection, err = s.selector.beginSelectionSessionForKey(ctx, route.Provider, route.ID, route.UpstreamModel, quotaMode, "", excluded, false, key.AccountScope())
			if err != nil {
				errorCode := "upstream_unavailable"
				var selectionFailure *SelectionUnavailableError
				if errors.As(err, &selectionFailure) {
					errorCode = selectionFailure.Code()
				}
				writeFailureAudit(http.StatusServiceUnavailable, errorCode, lastCredentialFailure)
				return nil, fmt.Errorf("%w: %w", ErrNoAvailableAccount, err)
			}
		}
		lease, err = selection.Acquire(ctx, excluded, false)
		if err != nil {
			errorCode := "upstream_unavailable"
			var selectionFailure *SelectionUnavailableError
			if errors.As(err, &selectionFailure) {
				errorCode = selectionFailure.Code()
			}
			writeFailureAudit(http.StatusServiceUnavailable, errorCode, lastCredentialFailure)
			return nil, fmt.Errorf("%w: %w", ErrNoAvailableAccount, err)
		}
		excluded[lease.Credential.ID] = true
		if !s.selector.ReserveQuota(lease, max(1, requestedCount)) {
			lease.Release()
			continue
		}
		credential, err = s.accounts.EnsureCredential(ctx, lease.Credential, false)
		if err != nil {
			s.logger.Error("image_credential_failed", "event_id", eventID, "request_id", requestID, "model", externalModel, "provider", route.Provider, "account_id", lease.Credential.ID, "error", err)
			recordAttempt(lease.Credential, audit.AttemptSourceCredential, "ensure_credential", attemptStarted, nil, err)
			failedCredential := lease.Credential
			lastCredentialFailure = &failedCredential
			lastCredentialError = err
			lease.Release()
			continue
		}
		lease.markSelectorUpstreamStarted()
		response, err = execute(ctx, route.Provider, credential, route.UpstreamModel)
		if err != nil {
			s.logger.Error("image_upstream_failed", "event_id", eventID, "request_id", requestID, "model", externalModel, "provider", route.Provider, "account_id", credential.ID, "error", err)
			recordAttempt(credential, audit.AttemptSourceTransport, "image_execute", attemptStarted, nil, err)
			if isSSOCredentialRejected(err, credential) {
				s.markSSOCredentialRejected(ctx, credential, fmt.Sprintf("%s SSO credential rejected", credential.Provider))
				failedCredential := credential
				lastCredentialFailure = &failedCredential
				lastCredentialError = provider.ErrUnauthorized
				lease.Release()
				continue
			}
			if provider.IsMediaPostProcessingError(err) {
				lease.Release()
				writeFailureAudit(http.StatusBadGateway, "media_postprocessing_failed", &credential)
				return nil, err
			}
			quotaRejected := provider.IsQuotaError(err)
			if !quotaRejected && lease.routingCandidate != nil && lease.routingCandidate.QuotaWindow != nil &&
				lease.routingCandidate.QuotaWindow.Remaining <= 1 && provider.IsQuotaMessage(err.Error()) {
				quotaRejected = true
			}
			// Only a quota rejection proves the account never started
			// generating, so it is the only transport error that may switch
			// accounts. Retrying any other failure could duplicate an image
			// that is already in flight upstream.
			if !quotaRejected {
				s.selector.MarkFailure(ctx, credential, 0, 0)
				lease.Release()
				writeFailureAudit(http.StatusBadGateway, "upstream_unavailable", &credential)
				return nil, err
			}
			failedCredential := credential
			lastCredentialFailure = &failedCredential
			lastCredentialError = err
			retryAfter := imageQuotaRetryAfter(lease, time.Now().UTC(), time.Duration(s.imageQuotaCooldown.Load()))
			lease.Release()
			s.selector.MarkModelQuotaExhausted(ctx, credential, lease.Billing, route.UpstreamModel, retryAfter)
			if earliestQuotaRetry == 0 || retryAfter < earliestQuotaRetry {
				earliestQuotaRetry = retryAfter
			}
			quotaExhausted = true
			continue
		}
		if response.StatusCode == http.StatusUnauthorized && credential.AuthType == accountdomain.AuthTypeSSO {
			_, _ = readRetryableBody(response.Body)
			s.markSSOCredentialRejected(ctx, credential, fmt.Sprintf("%s SSO credential rejected", credential.Provider))
			failedCredential := credential
			lastCredentialFailure = &failedCredential
			lastCredentialError = provider.ErrUnauthorized
			response = nil
			lease.Release()
			continue
		}
		if s.retireConsoleNoCreditsAccount(ctx, requestID, credential, response) {
			_, _ = readRetryableBody(response.Body)
			failedCredential := credential
			lastCredentialFailure = &failedCredential
			lastCredentialError = fmt.Errorf("%s", consoleNoCreditsReason)
			response = nil
			lease.Release()
			continue
		}
		if s.providers.RetryForbiddenAsEgress(credential.Provider) && response.StatusCode == http.StatusForbidden && attempt == 0 && attemptPolicy.hasNext(attempt) {
			_, _ = readRetryableBody(response.Body)
			delete(excluded, credential.ID)
			if selection != nil {
				selection.RetryAccount(credential.ID)
			}
			lease.Release()
			continue
		}
		if quotaKind, _ := s.providers.QuotaKind(credential.Provider); quotaKind == provider.QuotaRemoteWindow && response.StatusCode == http.StatusTooManyRequests && lease.QuotaMode != "" {
			retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), time.Now().UTC())
			exhausted, reconcileErr := s.accounts.ReconcileWebRateLimit(ctx, credential.ID, lease.QuotaMode, retryAfter)
			s.selector.MarkQuotaStateChanged(credential.Provider, credential.ID)
			if reconcileErr != nil || !exhausted {
				s.selector.MarkFailure(ctx, credential, response.StatusCode, retryAfter)
			}
			if attemptPolicy.hasNext(attempt) {
				_, _ = readRetryableBody(response.Body)
				lease.Release()
				continue
			}
		}
		break
	}
	if response == nil {
		if quotaExhausted {
			writeFailureAudit(http.StatusTooManyRequests, "upstream_quota_exhausted", lastCredentialFailure)
			return nil, &SelectionUnavailableError{Reason: SelectionQuotaExhausted, RetryAfter: earliestQuotaRetry}
		}
		writeFailureAudit(http.StatusServiceUnavailable, "upstream_unavailable", lastCredentialFailure)
		if lastCredentialError == nil {
			lastCredentialError = ErrNoAvailableAccount
		}
		return nil, fmt.Errorf("%w: %w", ErrNoAvailableAccount, lastCredentialError)
	}
	effectiveQuotaMode := lease.QuotaMode
	accountID := credential.ID
	var once sync.Once
	finalize := func(_ Usage, _ string, errorCode string) {
		once.Do(func() {
			successful := auditRequestSucceeded(response.StatusCode, errorCode)
			lease.completeSelectorObservation(successful)
			if successful {
				lease.commitQuotaReservation()
			}
			lease.Release()
			budget := newFinalizationBudget(string(operation), string(route.Provider))
			record := auditBase
			record.AccountID, record.AccountName, record.StatusCode = &accountID, credential.Name, response.StatusCode
			record.ErrorCode = errorCode
			record.AttemptCount = len(attempts)
			record.Attempts = append([]audit.Attempt(nil), attempts...)
			record.DurationMS, record.CreatedAt = time.Since(startedAt).Milliseconds(), time.Now().UTC()
			applyAuditEgress(&record, egressTrace, route.Provider)
			if successful {
				record.MediaOutputImages = int64(max(0, requestedCount))
				var pricing audit.PricingResult
				var priced bool
				switch operation {
				case audit.OperationImage:
					pricing, priced = audit.EstimateOfficialImageCost(pricingModel, pricingResolution, pricingQuality, requestedCount)
				case audit.OperationImageEdit:
					pricing, priced = audit.EstimateOfficialImageEditCost(pricingModel, pricingResolution, pricingQuality, requestedCount, inputImageCount)
				}
				if priced {
					record.EstimatedCostInUSDTicks = pricing.CostInUSDTicks
					record.PricingModel = pricing.Model
					record.PricingVersion = audit.OfficialPricingAsOf
				}
			}
			quotaKind, _ := s.providers.QuotaKind(route.Provider)
			refreshMode, decrementMode, availabilityMode := quotaFinalizationModes(effectiveQuotaMode, quotaRefreshGroup)
			if successful && quotaKind == provider.QuotaRemoteWindow && refreshMode != "" {
				if decrementMode != "" && decrementMode != "weekly" {
					units := max(1, response.QuotaUnits)
					var updated bool
					err := budget.run("quota_decrement", finalizationQuotaBudget, func(stageCtx context.Context) error {
						var decrementErr error
						updated, decrementErr = s.accounts.DecrementWebQuota(stageCtx, accountID, decrementMode, units)
						return decrementErr
					})
					if err != nil {
						s.logger.Warn("web_quota_decrement_failed", "account_id", accountID, "mode", decrementMode, "units", units, "error", err)
					} else if updated {
						s.selector.ConsumeQuota(route.Provider, accountID, decrementMode, units)
					}
				}
				s.accounts.QueueQuotaRefresh(accountID, refreshMode)
				if availabilityMode != "" && availabilityMode != refreshMode {
					s.accounts.QueueQuotaRefresh(accountID, availabilityMode)
				}
			}
			if err := budget.run("audit", finalizationAuditBudget, func(stageCtx context.Context) error {
				return s.audits.Create(stageCtx, record)
			}); err != nil {
				s.logger.Error("request_usage_write_failed", "event_id", record.EventID, "request_id", requestID, "error", err)
			}
		})
	}
	finalizationOwnsReservation = true
	return &Result{StatusCode: response.StatusCode, Status: response.Status, Header: response.Header, Body: &finalizingBody{ReadCloser: response.Body, finalize: func() { finalize(Usage{}, "", "stream_closed") }}, Finalize: finalize}, nil
}

// quotaFinalizationModes separates the immediate local consumption fence from
// the authoritative provider refresh. A refresh group may update several
// upstream windows atomically, while the local fence must charge the exact
// window selected for this account so concurrent media requests cannot
// over-allocate during the short refresh delay.
func quotaFinalizationModes(effectiveMode, refreshGroup string) (refreshMode, decrementMode, availabilityMode string) {
	// Availability-only Imagine products on paid Web tiers are governed by the
	// shared weekly pool. Refresh its numeric counter and also re-read the
	// product group so available=false/nextAvailableAt can install an exact
	// product fence that overrides weekly routing.
	if effectiveMode == "weekly" {
		return effectiveMode, effectiveMode, refreshGroup
	}
	refreshMode = effectiveMode
	if refreshGroup != "" {
		refreshMode = refreshGroup
	}
	return refreshMode, effectiveMode, ""
}
