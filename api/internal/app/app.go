// Copyright (C) 2026 Michael Kao
// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lenaxia/llmsafespaces/api/internal/config"
	"github.com/lenaxia/llmsafespaces/api/internal/handlers"
	"github.com/lenaxia/llmsafespaces/api/internal/logger"
	"github.com/lenaxia/llmsafespaces/api/internal/server"
	"github.com/lenaxia/llmsafespaces/api/internal/services"
	"github.com/lenaxia/llmsafespaces/api/internal/services/auth"
	"github.com/lenaxia/llmsafespaces/api/internal/services/cache"
	"github.com/lenaxia/llmsafespaces/api/internal/services/database"
	emailsvc "github.com/lenaxia/llmsafespaces/api/internal/services/email"
	"github.com/lenaxia/llmsafespaces/api/internal/services/metering"
	"github.com/lenaxia/llmsafespaces/api/internal/services/metrics"
	"github.com/lenaxia/llmsafespaces/api/internal/services/msgqueue"
	"github.com/lenaxia/llmsafespaces/api/internal/services/policy"
	"github.com/lenaxia/llmsafespaces/api/internal/services/sessionindex"
	"github.com/lenaxia/llmsafespaces/api/internal/services/sso"
	"github.com/lenaxia/llmsafespaces/api/internal/services/workspace"
	"github.com/lenaxia/llmsafespaces/api/internal/services/wsstate"
	agentoc "github.com/lenaxia/llmsafespaces/pkg/agent/opencode"
	"github.com/lenaxia/llmsafespaces/pkg/agentd"
	"github.com/lenaxia/llmsafespaces/pkg/billing"
	emailpkg "github.com/lenaxia/llmsafespaces/pkg/email"
	"github.com/lenaxia/llmsafespaces/pkg/kubernetes"
	"github.com/lenaxia/llmsafespaces/pkg/secrets"
	"github.com/lenaxia/llmsafespaces/pkg/settings"
	"github.com/lenaxia/llmsafespaces/pkg/types"
)

// Compile-time check that *WorkspaceClient satisfies the caller-shaped
// ModelClient interface (H2-a). If WorkspaceClient.ListModels or
// .PatchConfig signature drifts, this fails at build time instead of at
// the SetAgentClient call site.
var _ handlers.ModelClient = (*agentoc.WorkspaceClient)(nil)

type App struct {
	config             *config.Config
	logger             *logger.Logger
	router             *gin.Engine
	server             *http.Server
	k8sClient          *kubernetes.Client
	services           *services.Services
	proxyHandler       *handlers.ProxyHandler
	agentReloadHandler *handlers.AgentReloadHandler
	bulkReloadHandler  *handlers.BulkReloadHandler
	sessionIndexSvc    *sessionindex.Service
	instanceSettings   *settings.InstanceService
	userSettings       *settings.UserService
	asyncAudit         *secrets.AsyncAuditLogger // nil if pgxpool path not used
	secretsPool        *pgxpool.Pool             // pgx pool for secrets store; closed on shutdown
	dekCacheClient     *redis.Client             // redis client for DEK cache; closed on shutdown
	pendingOrgCleaner  *handlers.PendingOrgCleaner
	invitationsHandler *handlers.InvitationsHandler
	emailService       *emailsvc.Service
	emailHandler       *handlers.EmailHandler
	shutdownCh         chan struct{}
	ctx                context.Context
	cancel             context.CancelFunc
}

func New(cfg *config.Config, log *logger.Logger) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// validateMasterSecret is the very first check — before any infrastructure
	// is constructed. This ensures startup fails fast with a clear error rather
	// than a misleading K8s/DB error, and makes the enforcement unit-testable
	// without a live cluster (see TestApp_New_FailsWithoutMasterSecret).
	if err := validateMasterSecret(log); err != nil {
		cancel()
		return nil, err
	}

	k8sClient, err := kubernetes.New(&cfg.Kubernetes, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	svc, err := services.New(cfg, log, k8sClient)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to initialize services: %w", err)
	}

	proxyHandler, err := handlers.NewProxyHandler(k8sClient, log, cfg.Kubernetes.Namespace, nil, &agentoc.Dialect{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create proxy handler: %w", err)
	}
	proxyHandler.SetRequestBufferConfig(cfg.Proxy.RequestBufferSizePerWorkspace, time.Duration(cfg.Proxy.RequestBufferTimeoutSeconds)*time.Second)

	// Resolve subagent (subtask) sessions back to their root user-visible
	// session, so permission/question events from child sessions bubble up
	// to the chat view of the active parent session.
	proxyHandler.EnableSessionParentResolution()

	// Wire session index so sessions are tracked and listable.
	sessionIndexSvc := sessionindex.New(svc.Database, log)
	if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
		wsSvc.SetSessionIndex(sessionIndexSvc)
	}
	proxyHandler.SetSessionIndex(sessionIndexSvc)

	if cacheSvc, ok := svc.Cache.(*cache.Service); ok {
		queueSvc := msgqueue.NewWithClient(cacheSvc.GetClient())
		proxyHandler.SetMessageQueueService(queueSvc)

		// US-45.2..US-45.8: swap the in-memory state store for a Redis-backed
		// one so multi-replica deployments share all per-workspace state
		// (active sessions, deleted tombstones, password cache, workspace
		// config, prior phase, parent backfill).
		redisStateStore := wsstate.NewRedisStoreWithLogger(
			cacheSvc.GetClient(),
			wsstate.DefaultActiveSessTTL,
			log.With("component", "wsstate"),
		)
		proxyHandler.SetStateStore(redisStateStore)
	} else {
		// M4 (worklog 371): surface the silent fallback to InMemoryStore.
		// Without this warning, a future refactor that wraps the cache
		// service (so the *cache.Service type assertion fails) silently
		// reintroduces multi-replica drift: each replica keeps its own
		// activeSess / deletedSessions / pwCache, and the 2026-06-16
		// stuck-session incident class returns. Single-replica dev/test
		// deployments intentionally hit this path and can ignore the warning.
		log.Warn("Redis cache service unavailable — ProxyHandler is using InMemoryStore. Multi-replica deployments will NOT share per-workspace state (active sessions, tombstones, password cache). This is expected for single-replica dev/test; investigate in production.")
	}

	if svc.Metering != nil {
		proxyHandler.SetMeteringService(svc.Metering)
		if concrete, ok := svc.Metering.(*metering.Service); ok {
			concrete.SetDatabaseService(svc.Database)
			concrete.SetActivePhasesChecker(proxyHandler.GetAllKnownPhases)
		}
	}

	// Initialize settings services (backed by the same DB service).
	dbSvc := svc.Database.(*database.Service)
	instanceSettings := settings.NewInstanceService(dbSvc, log)
	userSettings := settings.NewUserService(dbSvc, log)

	// US-49.2: When email is helm-managed (email block present in config.yaml),
	// mark the email.* instance settings as read-only and pin their values
	// from the helm config. The admin UX will show them disabled with a
	// "Managed by Helm" badge; PUT attempts return 409.
	if cfg.Email.Provider != "" || cfg.Email.FromAddress != "" || cfg.Email.BaseURL != "" {
		instanceSettings.SetHelmOverrides(map[string]any{
			"email.provider":    cfg.Email.Provider,
			"email.sesRegion":   cfg.Email.SESRegion,
			"email.fromAddress": cfg.Email.FromAddress,
			"email.baseUrl":     cfg.Email.BaseURL,
		})
	}

	// Inject instance settings into workspace service for enforcement.
	if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
		wsSvc.SetInstanceSettings(instanceSettings)
	}

	// Wire version sync: whenever the watcher observes a workspace becoming
	// Active with a new imageTag, persist it to the DB immediately. This
	// replaces the lazy side-effect in GetWorkspaceStatus which only updated
	// the DB when the status endpoint was polled for that specific workspace.
	proxyHandler.SetVersionSyncCallback(func(workspaceID, imageTag, agentVersion string) {
		dbSvc.SyncWorkspaceVersionInfo(context.Background(), workspaceID, imageTag, agentVersion)
	})

	// Create settings handler for API routes.
	settingsHandler := handlers.NewSettingsHandler(instanceSettings, userSettings)

	// Wire secret management (Epic 10).
	var secretsHandler *handlers.SecretsHandler
	var modelsHandler *handlers.ModelsHandler
	var workspaceEnvHandler *handlers.WorkspaceEnvHandler
	var rotateKeyHandler *handlers.RotateKeyHandler
	var adminProvCredHandler *handlers.AdminProviderCredentialsHandler
	var userProvCredHandler *handlers.UserProviderCredentialsHandler
	var orgsHandler *handlers.OrgsHandler
	var orgCredsHandler *handlers.OrgCredentialsHandler
	var pgOrgStore *database.PgOrgStore
	var pendingOrgCleaner *handlers.PendingOrgCleaner
	var invitationsHandler *handlers.InvitationsHandler
	var emailService *emailsvc.Service
	var emailHandler *handlers.EmailHandler
	var passwordResetHandler *handlers.PasswordResetHandler
	var orgCredBinder *secrets.PgSecretStore
	var keyService *secrets.KeyService
	var policySvc *policy.Service
	var policyHandler *handlers.PolicyHandler
	var auditHandler *handlers.AuditHandler
	var platformAdminHandler *handlers.PlatformAdminHandler
	var internalOrgStatusHandler *handlers.InternalOrgStatusHandler
	var ssoHandler *handlers.SSOHandler
	var asyncAudit *secrets.AsyncAuditLogger // populated when secrets are enabled; drained on Shutdown
	var secretsPool *pgxpool.Pool            // closed on Shutdown
	var dekCacheClient *redis.Client         // closed on Shutdown
	{
		mk := dekMasterKey()
		if mk == nil {
			// Unreachable after validateMasterSecret passed — env var is
			// immutable for the process lifetime. Guards against future
			// refactors that move validateMasterSecret.
			cancel()
			return nil, errors.New("internal: dekMasterKey returned nil after validateMasterSecret passed")
		}
		dekCacheClient = redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		dekCache := secrets.NewRedisDEKCache(dekCacheClient, mk)

		// Create pgxpool for secret stores (same DB, separate pool for pgx native queries).
		pgxDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Database.Host, cfg.Database.Port, cfg.Database.User,
			cfg.Database.Password, cfg.Database.Database, cfg.Database.SSLMode)
		var pgxErr error
		secretsPool, pgxErr = pgxpool.New(context.Background(), pgxDSN)

		var secretService *secrets.SecretService
		var auditStore secrets.SecretStore
		if pgxErr != nil {
			// Refusing to start is the only correct response: the
			// in-memory adapter fallback (dbSecretStoreAdapter,
			// dbKeyStoreAdapter) is racy, unbounded in audit log
			// growth, and loses every secret + key on restart. It
			// existed for dev-environment convenience but in any
			// shape resembling production it is silent data loss
			// disguised as graceful degradation. Tests use the
			// in-memory adapters directly via NewSecretService;
			// production must always have pgxpool.
			cancel()
			return nil, fmt.Errorf("create pgxpool for secrets store: %w (refusing to fall back to in-memory; the in-memory secret/key adapters lose data on restart and are not safe for any environment that handles real user secrets)", pgxErr)
		}
		pgStore := secrets.NewPgSecretStore(secretsPool)
		orgCredBinder = pgStore
		// Wrap the secret store in an async audit logger so audit
		// writes do not block the request goroutine. The wrapper is
		// itself a SecretStore (CRUD methods delegate; LogAudit goes
		// through a 4096-entry buffered channel). Operators see drop
		// counts via Stats() and Warn-level logs.
		asyncAudit = secrets.NewAsyncAuditLogger(pgStore, 4096, log)
		keyService = secrets.NewKeyService(secrets.NewPgKeyStore(secretsPool), dekCache)
		keyService.SetLogger(log)
		secretService = secrets.NewSecretService(keyService, asyncAudit)
		auditStore = asyncAudit

		// M2-a: shared model cache between SecretsHandler (evicts on bind) and
		// ModelsHandler (reads on ListModels). One cache, two consumers.
		sharedModelCache := handlers.NewInMemoryModelCache()

		secretsHandler = handlers.NewSecretsHandler(secretService)
		secretsHandler.SetModelCache(sharedModelCache)
		// US-29.5: ModelsHandler extracted from SecretsHandler. AgentClient
		// is set later after proxyHandler is constructed (it depends on the
		// runtime password getter). Parser + cache are wired now so the
		// handler is functional for construction-time validation.
		modelsHandler = handlers.NewModelsHandler(nil) // agentClient wired below
		modelsHandler.SetModelCache(sharedModelCache)

		// Wire billing/metering metrics recorder.
		if metricsSvc, ok := svc.GetMetrics().(*metrics.Service); ok {
			modelsHandler.SetMetricsRecorder(metricsSvc)
		}
		// Epic 26: mark relay active when configured.
		if inferenceRelayURL := cfg.Server.InferenceRelayURL; inferenceRelayURL != "" {
			modelsHandler.SetRelayActive(true)
		}
		modelsHandler.SetLogger(log)
		modelsHandler.SetModelStore(dbSvc)
		if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
			modelsHandler.SetManifestWriter(wsSvc)
		}
		// US-29.4: WorkspaceEnvHandler owns the env-var endpoints.
		workspaceEnvHandler = handlers.NewWorkspaceEnvHandler(secretService)
		workspaceEnvHandler.SetLogger(log)
		adminProvCredHandler = handlers.NewAdminProviderCredentialsHandler(pgStore, deriveServerKey)
		adminProvCredHandler.SetAutoApplyStore(pgStore)
		userProvCredHandler = handlers.NewUserProviderCredentialsHandler(pgStore, pgStore, keyService, secrets.NewPgKeyStore(secretsPool))
		userProvCredHandler.SetCredentialStateWriter(dbSvc)

		// Seed the free-tier opencode credential (Epic 30 US-30.4).
		if err := ensureFreeTierCredential(context.Background(), pgStore, log); err != nil {
			log.Warn("free-tier credential seeding skipped", "error", err.Error())
		}
		// Wire pod-IP resolver so reload-secrets can reach in-pod agentd.
		// Without this the SecretsHandler returns 503 for every reload
		// request and the SetBindings auto-push silently no-ops; see
		// Bug 1 + Bug 2 in worklog 0085.
		secretsHandler.SetPodIPResolver(newSecretsPodIPResolver(
			&k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace},
			dbSvc,
			log,
		))
		secretsHandler.SetLogger(log)
		secretsHandler.SetCredentialStateWriter(dbSvc)
		// Wire password getter so ListModels/SetModel can authenticate
		// to opencode. Uses the same K8s-secret-backed getter as ProxyHandler.
		// Wired after proxyHandler construction (see below).
		// Wire the manifest writer so SetBindings persists a K8s Secret
		// (`workspace-secrets-<id>`) read by the pod init container on
		// every start. The live HTTP push alone is not durable; see
		// Bug 3 in worklog 0085.
		if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
			secretsHandler.SetSecretsManifestWriter(wsSvc)
		}
		// Wire the password verifier so RevealSecret enforces a real
		// re-authentication gate. Without this the field is theater
		// (validator finding on RevealSecret in worklog 0094 audit).
		if authSvc, ok := svc.Auth.(*auth.Service); ok {
			secretsHandler.SetPasswordVerifier(authSvc)
		}
		// Workspace-ownership enforcement for the bindings / env / reload-secrets
		// routes lives in WorkspaceAccessMiddleware (design 0041 D1+D5). The
		// SecretService trusts that decision and no longer carries its own
		// verifier — see pkg/secrets/secret_service.go.
		secretService.SetAdminKeyDeriver(deriveServerKey)
		rotateKeyHandler = handlers.NewRotateKeyHandler(keyService)
		rotateKeyHandler.SetPasswordUpdater(&bcryptPasswordUpdater{db: svc.Database})
		rotateKeyHandler.SetAuditFunc(func(userID, action string) {
			entry := &secrets.AuditEntry{
				UserID:    userID,
				Action:    action,
				Metadata:  []byte(`{}`),
				Timestamp: time.Now(),
			}
			_ = auditStore.LogAudit(context.Background(), entry)
		})

		rkp := newRootKeyProvider(cfg, log)

		if authSvc, ok := svc.Auth.(*auth.Service); ok {
			authSvc.SetKeyService(keyService)
			authSvc.SetInstanceSettings(instanceSettings)

			if rkp != nil {
				authSvc.SetRootKeyProvider(rkp)
			} else {
				authSvc.SetMasterKey(dekMasterKey())
			}
		}

		pgOrgStore = database.NewPgOrgStore(dbSvc.DB)
		orgsHandler = handlers.NewOrgsHandler(pgOrgStore, svc.GetAuth())
		orgCredsHandler = handlers.NewOrgCredentialsHandler(pgStore, pgStore, deriveServerKey, svc.GetAuth())

		// US-43.10: OIDC SSO. The service reuses the auth service as the JWT
		// issuer (GenerateToken) and the server KEK (RootKeyProvider) to encrypt
		// the IdP client secret (D17-S4). A dedicated state-signing key is
		// derived from the master secret so PKCE cookies are unforgeable.
		if authSvc, ok := svc.Auth.(*auth.Service); ok {
			stateKey := deriveServerKey("oidc-state-cookie")
			if stateKey != nil {
				ssoSvc, ssoErr := sso.New(pgOrgStore, dbSvc, sso.ServiceConfig{
					TokenIssuer:         authSvc,
					KeyProvider:         rkp,
					StateKey:            stateKey,
					TokenTTL:            cfg.Auth.TokenDuration,
					RedirectBaseURL:     cfg.OIDC.RedirectBaseURL,
					FrontendRedirectURL: cfg.OIDC.FrontendRedirectURL,
					StateCookieName:     cfg.OIDC.StateCookieName,
					Logger:              log,
				})
				if ssoErr != nil {
					log.Error("failed to construct sso service", ssoErr)
				} else {
					ssoHandler = handlers.NewSSOHandler(ssoSvc, pgOrgStore, svc.GetAuth(), cfg.Auth.CookieName, cfg.OIDC.FrontendRedirectURL, log)
				}
			}
		}

		// US-43.19: platform-admin suspension handlers. orgStore provides
		// UpdateOrgStatus + audit + the atomic last-admin-guarded suspend;
		// dbSvc provides SetUserStatus. svc.GetAuth() wires the F4 token
		// revocation primitive (MarkUserSuspended/ClearUserSuspended). log
		// surfaces best-effort audit-write + revocation-write failures.
		platformAdminHandler = handlers.NewPlatformAdminHandler(pgOrgStore, dbSvc, svc.GetAuth(), svc.GetAuth(), log)
		internalOrgStatusHandler = handlers.NewInternalOrgStatusHandler(pgOrgStore)

		if rkp != nil {
			keyService.SetAPIKeyStore(&apiKeyStoreAdapter{db: dbSvc}, rkp)
		} else {
			mk := dekMasterKey()
			if mk != nil {
				sp, _ := secrets.NewStaticKeyProvider(mk)
				keyService.SetAPIKeyStore(&apiKeyStoreAdapter{db: dbSvc}, sp)
			}
		}
		wsSvc, wsSvcOk := svc.Workspace.(*workspace.Service)
		if wsSvcOk {
			wsSvc.SetSecretInjector(secretService)
			wsSvc.SetCredentialProvisioner(pgStore)
			wsSvc.SetOrgStore(pgOrgStore)
		}
		// User provider-credential bind/unbind routes are NOT under
		// /api/v1/workspaces/:id (they live under /api/v1/provider-credentials/:id/bind/:workspaceId),
		// so WorkspaceAccessMiddleware does not cover them. Wire the
		// canonical ResolveWorkspace + CheckOwnership path so the
		// userProvCred surface shares the exact same authorisation
		// logic as every workspace route — including the D5
		// creator-membership re-check the old adapter lacked. If the
		// workspace service is somehow not the concrete type (defense-
		// in-depth — services.New always constructs *workspace.Service),
		// install a fail-closed checker that rejects every bind rather
		// than silently skipping the ownership check.
		if userProvCredHandler != nil {
			if wsSvcOk {
				userProvCredHandler.SetWorkspaceOwnerChecker(func(ctx context.Context, userID, wsID string) error {
					meta, err := wsSvc.ResolveWorkspace(ctx, wsID)
					if err != nil {
						return err
					}
					return wsSvc.CheckOwnership(ctx, userID, meta)
				})
			} else {
				log.Error("workspace service is not *workspace.Service; user provider-credential bind/unbind will fail-closed", nil)
				userProvCredHandler.SetWorkspaceOwnerChecker(func(_ context.Context, _, _ string) error {
					return fmt.Errorf("ownership verification unavailable: workspace service is misconfigured")
				})
			}
		}
	}

	// In development mode, disable RequireHTTPS so the API works over plain
	securityCfg := server.DefaultRouterConfig().SecurityConfig
	if cfg.Logging.Development {
		securityCfg.Development = true
		securityCfg.RequireHTTPS = false
		securityCfg.AllowHTTPSDowngrade = true
	}
	if len(cfg.Security.AllowedOrigins) > 0 {
		securityCfg.AllowedOrigins = cfg.Security.AllowedOrigins
	}
	securityCfg.AllowCredentials = cfg.Security.AllowCredentials

	rateLimitCfg := server.DefaultRouterConfig().RateLimitConfig
	rateLimitCfg.Enabled = cfg.RateLimiting.Enabled
	if cfg.RateLimiting.DefaultLimit > 0 {
		rateLimitCfg.DefaultLimit = cfg.RateLimiting.DefaultLimit
	}
	if cfg.RateLimiting.DefaultWindow > 0 {
		rateLimitCfg.DefaultWindow = cfg.RateLimiting.DefaultWindow
	}
	if cfg.RateLimiting.BurstSize > 0 {
		rateLimitCfg.BurstSize = cfg.RateLimiting.BurstSize
	}
	if cfg.RateLimiting.Strategy != "" {
		rateLimitCfg.Strategy = cfg.RateLimiting.Strategy
	}

	wsOrigins := server.DefaultRouterConfig().AllowedWebSocketOrigins
	if len(cfg.Security.AllowedOrigins) > 0 && cfg.Security.AllowedOrigins[0] != "*" {
		wsOrigins = cfg.Security.AllowedOrigins
	}

	// Create terminal handler (Epic 14 — WebSocket terminal proxy).
	terminalHandler := handlers.NewTerminalHandler(svc.Cache, &k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace}, cfg.Kubernetes.Namespace, log)

	// Epic 27a: Agent reload handler.
	var agentReloadHandler *handlers.AgentReloadHandler
	var bulkReloadHandler *handlers.BulkReloadHandler
	if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
		agentReloadHandler = handlers.NewAgentReloadHandler(
			wsSvc,
			dbSvc,
			newSecretsPodIPResolver(
				&k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace},
				dbSvc,
				log,
			),
			&http.Client{Timeout: 15 * time.Second},
			log,
		)
		bulkReloadHandler = handlers.NewBulkReloadHandler(
			dbSvc,
			wsSvc,
			dbSvc,
			newSecretsPodIPResolver(
				&k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace},
				dbSvc,
				log,
			),
			&http.Client{Timeout: 15 * time.Second},
			log,
		)
	}

	// Epic 27b: Drain mode SSETracker wiring is deferred to Run() — the tracker
	// is nil until proxyHandler.Start() runs. Wire password getter + metrics here
	// (these are available at construction time).
	if agentReloadHandler != nil {
		pwGetter := proxyHandler.GetPasswordGetter()
		agentReloadHandler.SetPasswordGetter(pwGetter)
		bulkReloadHandler.SetPasswordGetter(pwGetter)
		// US-29.5: construct ModelsHandler with AgentClient now that
		// the password getter is available.
		if modelsHandler != nil {
			ipResolver := newSecretsPodIPResolver(
				&k8sWorkspaceGetterAdapter{client: k8sClient, namespace: cfg.Kubernetes.Namespace},
				dbSvc, log,
			)
			pwAdapter := func(ctx context.Context, wsID string) (string, error) {
				return pwGetter.WorkspacePassword(ctx, wsID)
			}
			agentClient := agentoc.NewWorkspaceClient(pwAdapter, ipResolver, log.ZapLogger())
			modelsHandler.SetAgentClient(agentClient)
			if relayURL := cfg.Server.InferenceRelayURL; relayURL != "" {
				modelsHandler.SetRelayChecker(buildRelayChecker(ipResolver, pwAdapter))
			}
		}
	}
	// Wire metrics into reload handlers (guarded: handlers are nil when workspace
	// service type assertion fails, e.g. in tests or future refactors).
	if agentReloadHandler != nil {
		if metricsSvc, ok := svc.Metrics.(*metrics.Service); ok {
			agentReloadHandler.SetMetrics(metricsSvc)
			bulkReloadHandler.SetMetrics(metricsSvc)
		}
	}

	usageHandler := handlers.NewUsageHandler(svc.Metering, svc.Database)
	if dbSvc, ok := svc.Database.(*database.Service); ok {
		usageHandler.SetDB(dbSvc.DB)
	}

	// US-44.11: admin-only session recovery (force-abort stuck sessions).
	// Wired with the same *sql.DB handle as the usage handler so the audit
	// log INSERT shares the connection pool; nil DB is handled gracefully.
	var adminSessionHandler *handlers.AdminSessionHandler
	if dbSvc, ok := svc.Database.(*database.Service); ok {
		adminSessionHandler = handlers.NewAdminSessionHandler(proxyHandler, dbSvc.DB, log)
	} else {
		adminSessionHandler = handlers.NewAdminSessionHandler(proxyHandler, nil, log)
	}

	var checkoutProvider billing.CheckoutProvider
	var webhookHandler *handlers.StripeWebhookHandler
	if cfg.Billing.SecretKey != "" {
		sp, err := billing.NewStripeProvider(billing.StripeConfig{
			SecretKey:     cfg.Billing.SecretKey,
			WebhookSecret: cfg.Billing.WebhookSecret,
			PlanPrices:    cfg.Billing.PlanPrices,
			Meters:        cfg.Billing.Meters,
		})
		if err != nil {
			cancel()
			return nil, fmt.Errorf("init stripe provider: %w", err)
		}
		checkoutProvider = sp
		// US-43.17: Wire StripeProvider as usage reporter for metered billing.
		if mSvc, ok := svc.Metering.(*metering.Service); ok {
			mSvc.SetUsageReporter(sp)
		}
		if orgsHandler != nil && cfg.Billing.WebhookSecret != "" && pgOrgStore != nil {
			webhookHandler = handlers.NewStripeWebhookHandler(sp, pgOrgStore, log)
		}
		if orgsHandler != nil {
			orgsHandler.SetBilling(handlers.NewOrgBilling(checkoutProvider),
				cfg.Billing.CheckoutSuccessURL, cfg.Billing.CheckoutCancelURL, cfg.Billing.PortalReturnURL)
		}
	} else if orgsHandler != nil {
		noop := &billing.NoopCheckoutProvider{}
		orgsHandler.SetBilling(handlers.NewOrgBilling(noop),
			cfg.Billing.CheckoutSuccessURL, cfg.Billing.CheckoutCancelURL, cfg.Billing.PortalReturnURL)
	}

	// Pending org cleanup cron: reaps pending_activation orgs whose Stripe
	// checkout was never completed after 7 days. Only runs with a real Stripe
	// provider (needs checkout-session lookup); in dev mode without Stripe the
	// cleanup is a no-op (pending orgs accumulate but are harmless).
	if checkoutProvider != nil && pgOrgStore != nil {
		pendingOrgCleaner = handlers.NewPendingOrgCleaner(
			pgOrgStore, checkoutProvider, log, time.Hour, 7*24*time.Hour)
	}

	// Epic 49: email + password-reset wiring. Extracted into a helper to
	// keep New() under the funlen limit. The helper constructs the email
	// provider, EmailService, EmailHandler, and PasswordResetHandler.
	emailService, emailHandler, passwordResetHandler = initEmailStack(cfg, svc, dbSvc, keyService, log, cancel)

	// Invitations still needs the raw provider + the org store.
	if pgOrgStore != nil {
		mailer := resolveEmailProvider(cfg, cancel)
		invitationsHandler = handlers.NewInvitationsHandler(pgOrgStore, mailer, svc.GetAuth(), cfg.Email.BaseURL, log)
		if orgCredBinder != nil {
			invitationsHandler.SetCredentialBinder(orgCredBinder)
		}
	}

	// US-43.7: Org policy service + handler.
	if pgOrgStore != nil {
		policySvc = policy.New(pgOrgStore, svc.Cache)
		policyHandler = handlers.NewPolicyHandler(pgOrgStore, policySvc, svc.GetAuth(), log)
		if wsSvc, ok := svc.Workspace.(*workspace.Service); ok {
			wsSvc.SetPolicyChecker(policySvc)
		}
		// US-43.13: Org audit handler.
		auditHandler = handlers.NewAuditHandler(pgOrgStore)
	}

	// US-43.8: Wire policy checker into secrets handler for model filtering.
	if policySvc != nil && modelsHandler != nil {
		modelsHandler.SetPolicyChecker(policySvc)
	}

	relayRouterSvcURL := os.Getenv("RELAY_ROUTER_SVC_URL")
	if relayRouterSvcURL == "" {
		relayRouterSvcURL = "http://relay-router." + cfg.Kubernetes.Namespace + ".svc.cluster.local:8080"
	}
	routerNamespace := os.Getenv("LLMSAFESPACES_KUBERNETES_PODNAMESPACE")
	if routerNamespace == "" {
		routerNamespace = cfg.Kubernetes.Namespace
	}
	var relayAdminHandler *handlers.RelayAdminHandler
	if llmClient, err := k8sClient.LlmsafespacesV1(); err == nil {
		relayAdminHandler = handlers.NewRelayAdminHandler(
			k8sClient.Clientset(),
			llmClient,
			cfg.Kubernetes.Namespace,
			routerNamespace,
			relayRouterSvcURL,
		)
	} else {
		log.Warn("failed to construct LlmsafespacesV1 client, relay admin routes will not be available", "error", err.Error())
	}

	router := server.NewRouter(svc, log, proxyHandler, server.RouterConfig{
		Debug:                           cfg.Logging.Development,
		LoggingConfig:                   server.DefaultRouterConfig().LoggingConfig,
		RateLimitConfig:                 rateLimitCfg,
		SecurityConfig:                  securityCfg,
		TracingConfig:                   server.DefaultRouterConfig().TracingConfig,
		AllowedWebSocketOrigins:         wsOrigins,
		SettingsHandler:                 settingsHandler,
		InstanceSettings:                instanceSettings,
		AdminProviderCredentialsHandler: adminProvCredHandler,
		UserProviderCredentialsHandler:  userProvCredHandler,
		SecretsHandler:                  secretsHandler,
		ModelsHandler:                   modelsHandler,
		WorkspaceEnvHandler:             workspaceEnvHandler,
		RotateKeyHandler:                rotateKeyHandler,
		OrgsHandler:                     orgsHandler,
		OrgCredentialsHandler:           orgCredsHandler,
		TerminalHandler:                 terminalHandler,
		AgentReloadHandler:              agentReloadHandler,
		BulkReloadHandler:               bulkReloadHandler,
		UsageHandler:                    usageHandler,
		WebhookHandler:                  webhookHandler,
		InvitationsHandler:              invitationsHandler,
		EmailHandler:                    emailHandler,
		PasswordResetHandler:            passwordResetHandler,
		PolicyHandler:                   policyHandler,
		AuditHandler:                    auditHandler,
		RelayAdminHandler:               relayAdminHandler,
		AdminSessionHandler:             adminSessionHandler,
		PlatformAdminHandler:            platformAdminHandler,
		InternalOrgStatusHandler:        internalOrgStatusHandler,
		SSOHandler:                      ssoHandler,
		CookieName:                      cfg.Auth.CookieName,
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: router,
		// Slowloris hardening: cap header read time. Body read +
		// response write are bounded by per-handler logic; the API has
		// long-lived SSE endpoints so we deliberately do NOT set
		// ReadTimeout/WriteTimeout at the server level.
		ReadHeaderTimeout: 10 * time.Second,
	}

	return &App{
		config:             cfg,
		logger:             log,
		router:             router,
		server:             httpServer,
		k8sClient:          k8sClient,
		services:           svc,
		proxyHandler:       proxyHandler,
		agentReloadHandler: agentReloadHandler,
		bulkReloadHandler:  bulkReloadHandler,
		sessionIndexSvc:    sessionIndexSvc,
		instanceSettings:   instanceSettings,
		userSettings:       userSettings,
		asyncAudit:         asyncAudit,
		secretsPool:        secretsPool,
		pendingOrgCleaner:  pendingOrgCleaner,
		invitationsHandler: invitationsHandler,
		emailService:       emailService,
		emailHandler:       emailHandler,
		dekCacheClient:     dekCacheClient,
		shutdownCh:         make(chan struct{}),
		ctx:                ctx,
		cancel:             cancel,
	}, nil
}

func (a *App) Run() error {
	if err := a.services.Start(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Disabled: self-service org creation removed. Re-enable when billing portal ships.
	// if a.pendingOrgCleaner != nil {
	// 	go a.pendingOrgCleaner.Run(a.ctx)
	// 	a.logger.Info("pending org cleanup cron started", "interval", "1h", "maxAge", "7d")
	// }

	// Start instance settings (loads cache from DB).
	if err := a.instanceSettings.Start(); err != nil {
		a.logger.Warn("Instance settings failed to start (will use defaults)", "error", err.Error())
		// Non-fatal: settings will fall back to schema defaults.
	}

	// Seed instance settings defaults (idempotent).
	if result, err := settings.Seed(a.ctx, a.services.Database.(*database.Service), a.logger); err != nil {
		a.logger.Warn("Settings seed failed", "error", err.Error())
	} else {
		a.logger.Info("Settings seed complete", "inserted", result.Inserted, "skipped", result.Skipped, "orphaned", len(result.Orphaned))
	}

	if err := a.k8sClient.Start(); err != nil {
		_ = a.services.Stop()
		return fmt.Errorf("failed to start Kubernetes client: %w", err)
	}

	if err := a.proxyHandler.Start(); err != nil {
		a.k8sClient.Stop()
		_ = a.services.Stop()
		return fmt.Errorf("failed to start proxy handler: %w", err)
	}

	// Epic 27a/27b: Wire drain mode dependencies now that proxyHandler.Start()
	// has initialized the SSETracker.
	if a.agentReloadHandler != nil {
		if tracker := a.proxyHandler.GetSSETracker(); tracker != nil {
			a.agentReloadHandler.SetSSETracker(tracker)
			if a.bulkReloadHandler != nil {
				a.bulkReloadHandler.SetSSETracker(tracker)
			}
		}
		// Wire queue clearer and broker so dispose clears pending queue messages.
		if qs := a.proxyHandler.GetMessageQueueService(); qs != nil {
			a.agentReloadHandler.SetQueueClearer(qs)
			if a.bulkReloadHandler != nil {
				a.bulkReloadHandler.SetQueueClearer(qs)
			}
		}
		if b := a.proxyHandler.GetBroker(); b != nil {
			a.agentReloadHandler.SetBrokerPublisher(b)
			if a.bulkReloadHandler != nil {
				a.bulkReloadHandler.SetBrokerPublisher(b)
			}
		}
	}

	// Epic 26 / billing: wire inference callback and session metrics unconditionally.
	// Previously nested inside the agentReloadHandler guard, which meant if the
	// workspace service type assertion failed (or the handler wasn't created),
	// SetOnInference was never called and inference metrics remained permanently zero.
	if tracker := a.proxyHandler.GetSSETracker(); tracker != nil {
		if metricsSvc, ok := a.services.Metrics.(*metrics.Service); ok {
			meteringSvc := a.services.Metering
			ph := a.proxyHandler
			tracker.SetOnInference(func(workspaceID, modelID, providerID string, inputTokens, outputTokens int64, costDollars float64) {
				metricsSvc.RecordInference(modelID, providerID, inputTokens, outputTokens, costDollars)
				if meteringSvc == nil {
					return
				}
				ownerID := ph.GetWorkspaceOwner(workspaceID)
				if ownerID == "" {
					return
				}
				owner := types.BillingOwner{ID: ownerID, Type: types.OwnerTypeUser}
				meteringSvc.Record(types.UsageEvent{
					IdempotencyKey: fmt.Sprintf("tokens:%s:%s:in:%d", workspaceID, modelID, time.Now().UnixNano()),
					Owner:          owner,
					ActorID:        ownerID,
					WorkspaceID:    workspaceID,
					EventType:      "llm_tokens",
					EventSubtype:   "input",
					Quantity:       inputTokens,
					Source:         "api",
					EventTime:      time.Now(),
					Metadata:       map[string]any{"model_id": modelID, "provider_id": providerID},
				})
				if outputTokens > 0 {
					meteringSvc.Record(types.UsageEvent{
						IdempotencyKey: fmt.Sprintf("tokens:%s:%s:out:%d", workspaceID, modelID, time.Now().UnixNano()),
						Owner:          owner,
						ActorID:        ownerID,
						WorkspaceID:    workspaceID,
						EventType:      "llm_tokens",
						EventSubtype:   "output",
						Quantity:       outputTokens,
						Source:         "api",
						EventTime:      time.Now(),
						Metadata:       map[string]any{"model_id": modelID, "provider_id": providerID},
					})
				}
			})
			tracker.SetSessionMetrics(metricsSvc)
		}
	}
	// Epic 27b US-27b.5: Wire agent state checker into proxy for chat error enrichment.
	// dbSvc is referenced via services; use a type assertion to get the concrete type
	// which implements AgentStateChecker (GetLastCredentialChangedAt).
	if dbSvc, ok := a.services.Database.(*database.Service); ok {
		a.proxyHandler.SetAgentStateChecker(dbSvc)
	}

	if err := a.sessionIndexSvc.Start(); err != nil {
		_ = a.proxyHandler.Stop()
		a.k8sClient.Stop()
		_ = a.services.Stop()
		return fmt.Errorf("failed to start session index: %w", err)
	}

	a.logger.Info("Starting HTTP server", "address", a.server.Addr)

	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func (a *App) Shutdown() error {
	a.logger.Info("Shutting down application")

	a.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), a.config.Server.ShutdownTimeout)
	defer cancel()

	if err := a.server.Shutdown(ctx); err != nil {
		a.logger.Error("HTTP server shutdown error", err)
	}

	if err := a.proxyHandler.Stop(); err != nil {
		a.logger.Error("Proxy handler shutdown error", err)
	}

	if err := a.sessionIndexSvc.Stop(); err != nil {
		a.logger.Error("Session index shutdown error", err)
	}

	// Drain pending audit entries before tearing down the DB pool so
	// pending writes get a fair chance to land.
	if a.asyncAudit != nil {
		a.asyncAudit.Stop()
		stats := a.asyncAudit.Stats()
		a.logger.Info("Async audit logger drained",
			"written", stats.Written, "dropped", stats.Dropped, "failed", stats.Failed)
	}

	// Close the secrets pgxpool and Redis DEK cache last so any
	// last-millisecond audit write through asyncAudit.run() above
	// could complete. Both are nil-safe; we still nil-check for
	// belt-and-braces against future "secrets disabled" config paths.
	if a.secretsPool != nil {
		a.secretsPool.Close()
	}
	if a.dekCacheClient != nil {
		if err := a.dekCacheClient.Close(); err != nil {
			a.logger.Error("Redis DEK cache close error", err)
		}
	}

	a.k8sClient.Stop()

	if err := a.services.Stop(); err != nil {
		a.logger.Error("Services shutdown error", err)
	}

	a.logger.Info("Application shutdown complete")
	return nil
}

// validateMasterSecret verifies LLMSAFESPACES_MASTER_SECRET is present and
// decodes to at least 32 bytes (the AES-256-GCM key size minimum).
//
// Returns nil on success. Logs a structured Warn when the secret is present
// but too short so operators can distinguish "forgot to set it" from "set it
// to the wrong value." The secret value itself is never logged.
//
// This function reads the env var independently of deriveServerKey to keep
// deriveServerKey a pure, side-effect-free function compatible with the
// secrets.AdminKeyDeriver type (func(string) []byte).
func validateMasterSecret(log *logger.Logger) error {
	masterRaw := os.Getenv("LLMSAFESPACES_MASTER_SECRET")
	if masterRaw == "" {
		masterRaw = os.Getenv("LLMSAFESPACES_DEK_MASTER_KEY")
	}
	if masterRaw == "" {
		return errors.New(
			"LLMSAFESPACES_MASTER_SECRET is required but not set; " +
				"refusing to start without DEK encryption at rest in Redis. " +
				"Generate one with: openssl rand -hex 32")
	}

	var master []byte
	if decoded, err := hex.DecodeString(masterRaw); err == nil {
		master = decoded
	} else {
		master = []byte(masterRaw)
	}

	if len(master) < 32 {
		log.Warn("LLMSAFESPACES_MASTER_SECRET is set but too short for AES-256-GCM",
			"decoded_bytes", len(master), "required_bytes", 32)
		// masterRaw is intentionally NOT included in the error message or log.
		return fmt.Errorf(
			"LLMSAFESPACES_MASTER_SECRET decodes to %d bytes; minimum is 32 (AES-256-GCM key size). "+
				"Use at least 32 bytes (e.g. 64 hex chars, or 32+ alphanumeric chars)",
			len(master))
	}
	return nil
}

// buildRelayChecker creates a RelayStateChecker that reads the relay
// injection state from the agentd admin port (/v1/readyz). The checker
// resolves podIP + password internally, keeping the ModelsHandler free
// of pod/auth concerns (US-29.5 design).
func buildRelayChecker(
	ipResolver handlers.PodIPResolver,
	pwGetter func(context.Context, string) (string, error),
) handlers.RelayStateChecker {
	return newRelayChecker(&http.Client{Timeout: 5 * time.Second}, agentd.AgentdAdminPort, ipResolver, pwGetter)
}

// readyzReadLimit bounds the /v1/readyz response read. readyz is a tiny
// envelope (a bool plus small fields); 16 KiB is ample and matches the
// precedent set by the statusz decoder in proxy_events.go. Worklog 0372
// (H4): the limit was dropped during the US-29.5 extraction, leaving the
// decoder exposed to an unbounded body.
const readyzReadLimit = 16 * 1024

// newRelayChecker is the testable core of buildRelayChecker. The port and
// http.Client are injected so tests can target an httptest server and
// verify the read limit without binding the real agentd admin port.
func newRelayChecker(
	client *http.Client,
	port int,
	ipResolver handlers.PodIPResolver,
	pwGetter func(context.Context, string) (string, error),
) handlers.RelayStateChecker {
	return func(ctx context.Context, userID, workspaceID string) bool {
		podIP, err := ipResolver.GetWorkspacePodIP(ctx, userID, workspaceID)
		if err != nil || podIP == "" {
			return false
		}
		password, err := pwGetter(ctx, workspaceID)
		if err != nil || password == "" {
			return false
		}
		url := fmt.Sprintf("http://%s:%d/v1/readyz", podIP, port)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false
		}
		req.Header.Set("Authorization", "Bearer "+password)
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var readyz struct {
			RelayInjected bool `json:"relay_injected"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, readyzReadLimit)).Decode(&readyz) != nil {
			return false
		}
		return readyz.RelayInjected
	}
}

// resolveEmailProvider constructs the email provider based on config. Returns
// nil for the noop case (NoopProvider). Used by initEmailStack and the
// invitations handler wiring.
func resolveEmailProvider(cfg *config.Config, cancel context.CancelFunc) emailpkg.EmailProvider {
	switch strings.ToLower(cfg.Email.Provider) {
	case "ses":
		if cfg.Email.FromAddress == "" || cfg.Email.BaseURL == "" {
			cancel()
			return nil
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
			awsconfig.WithRegion(cfg.Email.SESRegion))
		if err != nil {
			cancel()
			return nil
		}
		return emailpkg.NewSESProvider(awsCfg, cfg.Email.FromAddress)
	default:
		return &emailpkg.NoopProvider{}
	}
}

// initEmailStack constructs the EmailService, EmailHandler, and
// PasswordResetHandler. Extracted from New() to keep it under the funlen
// limit. Returns (emailService, emailHandler, passwordResetHandler).
func initEmailStack(
	cfg *config.Config,
	svc *services.Services,
	dbSvc *database.Service,
	keyService *secrets.KeyService,
	log *logger.Logger,
	cancel context.CancelFunc,
) (*emailsvc.Service, *handlers.EmailHandler, *handlers.PasswordResetHandler) {
	mailer := resolveEmailProvider(cfg, cancel)
	emailService := emailsvc.NewService(mailer, cfg.Email.BaseURL, cfg.Email.Provider)
	emailHandler := handlers.NewEmailHandler(emailService, svc.GetRateLimiter(), log)

	emailTokenStore := database.NewPgEmailTokenStore(dbSvc.DB)
	var sessionRevoker interface {
		RevokeAllUserSessions(userID string) error
	}
	if authSvc, ok := svc.GetAuth().(*auth.Service); ok {
		sessionRevoker = authSvc
	}
	passwordResetHandler := handlers.NewPasswordResetHandler(
		emailTokenStore,
		svc.Database,
		keyService,
		&bcryptPasswordUpdater{db: svc.Database},
		sessionRevoker,
		emailService,
		log,
	)
	return emailService, emailHandler, passwordResetHandler
}
