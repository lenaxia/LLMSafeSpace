# API Service Design for LLMSafeSpace

## Overview

The API Service is a critical component of LLMSafeSpace that serves as the entry point for all SDK interactions. It exposes REST API and WebSocket endpoints for client communication, handles authentication and authorization, manages sandbox lifecycle operations, and coordinates with the Sandbox Controller for resource management.

This document provides a detailed low-level design for the API Service, including its architecture, components, interfaces, and implementation details.

## Architecture

### High-Level Architecture

The API Service follows a layered architecture with clear separation of concerns:

```
┌─────────────────────────────────────────────────────────────┐
│                      API Layer                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  REST API   │  │ WebSocket   │  │ Authentication &    │  │
│  │  Endpoints  │  │   Server    │  │   Authorization     │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
├─────────────────────────────────────────────────────────────┤
│                    Service Layer                            │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  Sandbox    │  │  Warm Pool  │  │  Execution          │  │
│  │  Service    │  │  Service    │  │  Service            │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
├─────────────────────────────────────────────────────────────┤
│                 Integration Layer                           │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ Kubernetes  │  │  Database   │  │  Cache              │  │
│  │ Client      │  │  Client     │  │  Client             │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

1. **API Layer**:
   - Handles HTTP and WebSocket requests/responses
   - Validates input data
   - Manages authentication and authorization
   - Routes requests to appropriate service layer components
   - Formats responses and handles errors

2. **Service Layer**:
   - Implements business logic for sandbox management
   - Coordinates warm pool allocation
   - Manages execution of code and commands
   - Handles file operations
   - Implements session management

3. **Integration Layer**:
   - Communicates with Kubernetes API to manage custom resources
   - Interacts with PostgreSQL for persistent storage
   - Uses Redis for caching and session management
   - Implements connection pooling and retry logic

## Detailed Component Design

### 1. API Layer

#### 1.1 REST API Endpoints

The REST API endpoints are implemented using a modern web framework (e.g., Go's Gin or Echo) with the following structure:

```go
// SandboxHandler handles sandbox-related API endpoints
type SandboxHandler struct {
    sandboxService    SandboxService
    authService       AuthService
    validationService ValidationService
}

// Routes registers all sandbox routes
func (h *SandboxHandler) Routes(router *gin.Engine) {
    sandboxGroup := router.Group("/api/v1/sandboxes")
    sandboxGroup.Use(h.authService.AuthMiddleware())
    
    sandboxGroup.GET("", h.ListSandboxes)
    sandboxGroup.POST("", h.CreateSandbox)
    sandboxGroup.GET("/:id", h.GetSandbox)
    sandboxGroup.DELETE("/:id", h.TerminateSandbox)
    sandboxGroup.GET("/:id/status", h.GetSandboxStatus)
    sandboxGroup.POST("/:id/execute", h.ExecuteCode)
    sandboxGroup.GET("/:id/files", h.ListFiles)
    sandboxGroup.GET("/:id/files/*path", h.DownloadFile)
    sandboxGroup.PUT("/:id/files/*path", h.UploadFile)
    sandboxGroup.DELETE("/:id/files/*path", h.DeleteFile)
    sandboxGroup.POST("/:id/packages", h.InstallPackages)
}
```

Each handler method follows a consistent pattern:
1. Extract and validate path/query parameters
2. Parse and validate request body
3. Check authorization for the requested resource
4. Call the appropriate service method
5. Format and return the response

#### 1.2 WebSocket Server

The WebSocket server handles real-time communication for streaming execution outputs:

```go
// WebSocketHandler manages WebSocket connections
type WebSocketHandler struct {
    sandboxService    SandboxService
    authService       AuthService
    sessionManager    SessionManager
}

// HandleConnection handles a new WebSocket connection
func (h *WebSocketHandler) HandleConnection(c *gin.Context) {
    // Upgrade HTTP connection to WebSocket
    conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        return
    }
    defer conn.Close()
    
    // Authenticate connection
    userID, err := h.authService.AuthenticateWebSocket(c)
    if err != nil {
        conn.WriteJSON(ErrorMessage{Type: "error", Code: "unauthorized"})
        return
    }
    
    // Create session
    session := h.sessionManager.CreateSession(userID, conn)
    defer h.sessionManager.CloseSession(session.ID)
    
    // Handle messages
    for {
        messageType, message, err := conn.ReadMessage()
        if err != nil {
            break
        }
        
        // Process message
        h.handleMessage(session, messageType, message)
    }
}
```

The WebSocket handler supports the message types defined in the API Design document, including execution requests, cancellation, and heartbeats.

#### 1.3 Authentication & Authorization

Authentication and authorization are implemented with the following components:

```go
// AuthService handles authentication and authorization
type AuthService struct {
    userRepository UserRepository
    tokenManager   TokenManager
    rbacManager    RBACManager
}

// AuthMiddleware returns a middleware function for authentication
func (s *AuthService) AuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // Extract token from Authorization header
        token := extractToken(c)
        
        // Validate token
        userID, err := s.tokenManager.ValidateToken(token)
        if err != nil {
            c.AbortWithStatusJSON(401, ErrorResponse{
                Error: ErrorDetails{
                    Code:    "unauthorized",
                    Message: "Invalid or expired token",
                },
            })
            return
        }
        
        // Store user ID in context
        c.Set("userID", userID)
        c.Next()
    }
}

// CheckResourceAccess verifies if a user has access to a resource
func (s *AuthService) CheckResourceAccess(userID, resourceType, resourceID string, action string) bool {
    // Check resource ownership
    isOwner := s.userRepository.CheckResourceOwnership(userID, resourceType, resourceID)
    if isOwner {
        return true
    }
    
    // Check RBAC permissions
    return s.rbacManager.CheckPermission(userID, resourceType, resourceID, action)
}
```

### 2. Service Layer

#### 2.1 Sandbox Service

The Sandbox Service implements the business logic for sandbox management:

```go
// SandboxService handles sandbox operations
type SandboxService struct {
    k8sClient        kubernetes.Interface
    warmPoolService  WarmPoolService
    databaseClient   DatabaseClient
    cacheClient      CacheClient
    executionService ExecutionService
    fileService      FileService
}

// CreateSandbox creates a new sandbox
func (s *SandboxService) CreateSandbox(ctx context.Context, req CreateSandboxRequest) (*Sandbox, error) {
    // Check if warm pool should be used
    var warmPodRef *WarmPodReference
    if req.UseWarmPool {
        // Try to get a warm pod
        warmPod, err := s.warmPoolService.AllocateWarmPod(ctx, req.Runtime, req.SecurityLevel)
        if err == nil && warmPod != nil {
            warmPodRef = &WarmPodReference{
                Name:      warmPod.Name,
                Namespace: warmPod.Namespace,
            }
        }
    }
    
    // Create Sandbox custom resource
    sandbox := &v1.Sandbox{
        ObjectMeta: metav1.ObjectMeta{
            GenerateName: "sb-",
            Labels: map[string]string{
                "app": "llmsafespace",
                "user-id": req.UserID,
            },
        },
        Spec: v1.SandboxSpec{
            Runtime:       req.Runtime,
            SecurityLevel: req.SecurityLevel,
            Timeout:       req.Timeout,
            Resources:     req.Resources,
            NetworkAccess: req.NetworkAccess,
        },
    }
    
    // Set warm pod reference if available
    if warmPodRef != nil {
        sandbox.Spec.WarmPodRef = warmPodRef
    }
    
    // Create the sandbox in Kubernetes
    result, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Create(ctx, sandbox, metav1.CreateOptions{})
    if err != nil {
        return nil, err
    }
    
    // Store sandbox metadata in database
    sandboxMeta := &SandboxMetadata{
        ID:        result.Name,
        UserID:    req.UserID,
        CreatedAt: time.Now(),
        Runtime:   req.Runtime,
    }
    err = s.databaseClient.CreateSandboxMetadata(ctx, sandboxMeta)
    if err != nil {
        // Attempt to clean up the Kubernetes resource
        _ = s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Delete(ctx, result.Name, metav1.DeleteOptions{})
        return nil, err
    }
    
    // Return sandbox details
    return &Sandbox{
        ID:        result.Name,
        Status:    string(result.Status.Phase),
        CreatedAt: result.CreationTimestamp.Time,
        Runtime:   req.Runtime,
        // ... other fields
    }, nil
}
```

#### 2.2 Warm Pool Service

The Warm Pool Service manages warm pool allocation and coordination:

```go
// WarmPoolService handles warm pool operations
type WarmPoolService struct {
    k8sClient      kubernetes.Interface
    databaseClient DatabaseClient
    cacheClient    CacheClient
}

// AllocateWarmPod finds and allocates a warm pod for a sandbox
func (s *WarmPoolService) AllocateWarmPod(ctx context.Context, runtime, securityLevel string) (*WarmPod, error) {
    // Check cache first for faster allocation
    cacheKey := fmt.Sprintf("warmpod:%s:%s", runtime, securityLevel)
    if podJSON, err := s.cacheClient.Get(ctx, cacheKey); err == nil {
        var pod WarmPod
        if err := json.Unmarshal([]byte(podJSON), &pod); err == nil {
            // Attempt to claim this pod
            claimed, err := s.claimWarmPod(ctx, &pod)
            if err == nil && claimed {
                return &pod, nil
            }
            // If claiming failed, remove from cache
            s.cacheClient.Delete(ctx, cacheKey)
        }
    }
    
    // Find available warm pods from Kubernetes
    selector := labels.SelectorFromSet(labels.Set{
        "runtime": strings.Replace(runtime, ":", "-", -1),
        "security-level": securityLevel,
        "status": "ready",
    })
    
    warmPods, err := s.k8sClient.LlmsafespaceV1().WarmPods("").List(ctx, metav1.ListOptions{
        LabelSelector: selector.String(),
        Limit:         10,
    })
    
    if err != nil || len(warmPods.Items) == 0 {
        return nil, errors.New("no warm pods available")
    }
    
    // Try to claim each pod until successful
    for _, pod := range warmPods.Items {
        warmPod := &WarmPod{
            Name:      pod.Name,
            Namespace: pod.Namespace,
            Runtime:   runtime,
        }
        
        claimed, err := s.claimWarmPod(ctx, warmPod)
        if err == nil && claimed {
            // Cache this successful allocation for future reference
            if podJSON, err := json.Marshal(warmPod); err == nil {
                s.cacheClient.SetWithTTL(ctx, cacheKey, string(podJSON), 30*time.Second)
            }
            return warmPod, nil
        }
    }
    
    return nil, errors.New("failed to allocate warm pod")
}

// claimWarmPod attempts to claim a warm pod for use
func (s *WarmPoolService) claimWarmPod(ctx context.Context, pod *WarmPod) (bool, error) {
    // Get the warm pod
    warmPod, err := s.k8sClient.LlmsafespaceV1().WarmPods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
    if err != nil {
        return false, err
    }
    
    // Check if pod is still available
    if warmPod.Status.Phase != "Ready" {
        return false, nil
    }
    
    // Try to update status to Assigned using optimistic concurrency
    warmPod.Status.Phase = "Assigned"
    warmPod.Status.AssignedAt = metav1.Now()
    
    _, err = s.k8sClient.LlmsafespaceV1().WarmPods(pod.Namespace).UpdateStatus(ctx, warmPod, metav1.UpdateOptions{})
    if err != nil {
        return false, err
    }
    
    return true, nil
}
```

#### 2.3 Execution Service

The Execution Service handles code and command execution:

```go
// ExecutionService handles code and command execution
type ExecutionService struct {
    k8sClient      kubernetes.Interface
    sessionManager SessionManager
}

// ExecuteCode executes code in a sandbox
func (s *ExecutionService) ExecuteCode(ctx context.Context, req ExecuteCodeRequest) (*ExecutionResult, error) {
    // Get sandbox
    sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Get(ctx, req.SandboxID, metav1.GetOptions{})
    if err != nil {
        return nil, err
    }
    
    // Check if sandbox is running
    if sandbox.Status.Phase != "Running" {
        return nil, errors.New("sandbox is not running")
    }
    
    // Create execution request
    execReq := &v1.ExecutionRequest{
        Type:    req.Type,
        Content: req.Content,
        Timeout: req.Timeout,
    }
    
    // Execute code via Kubernetes API
    execResult, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Execute(ctx, req.SandboxID, execReq)
    if err != nil {
        return nil, err
    }
    
    // Return execution result
    return &ExecutionResult{
        ExecutionID:  execResult.Name,
        Status:       string(execResult.Status.Phase),
        StartedAt:    execResult.Status.StartTime.Time,
        CompletedAt:  execResult.Status.CompletionTime.Time,
        ExitCode:     execResult.Status.ExitCode,
        Stdout:       execResult.Status.Stdout,
        Stderr:       execResult.Status.Stderr,
    }, nil
}

// StreamExecution executes code/command and streams the output
func (s *ExecutionService) StreamExecution(session *Session, req StreamExecutionRequest) {
    // Create execution context with cancellation
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    
    // Store cancellation function in session
    session.SetCancellationFunc(req.ExecutionID, cancel)
    
    // Notify client that execution has started
    session.Send(WebSocketMessage{
        Type:        "execution_start",
        ExecutionID: req.ExecutionID,
        Timestamp:   time.Now().UnixMilli(),
    })
    
    // Execute in goroutine
    go func() {
        // Create execution request
        execReq := &v1.ExecutionRequest{
            Type:    req.Mode,
            Content: req.Content,
            Timeout: req.Timeout,
            Stream:  true,
        }
        
        // Execute and stream
        stream, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).ExecuteStream(
            ctx, req.SandboxID, execReq)
        if err != nil {
            session.Send(WebSocketMessage{
                Type:        "error",
                Code:        "execution_failed",
                Message:     err.Error(),
                ExecutionID: req.ExecutionID,
                Timestamp:   time.Now().UnixMilli(),
            })
            return
        }
        
        // Process stream
        for {
            output, err := stream.Recv()
            if err == io.EOF {
                break
            }
            if err != nil {
                session.Send(WebSocketMessage{
                    Type:        "error",
                    Code:        "stream_error",
                    Message:     err.Error(),
                    ExecutionID: req.ExecutionID,
                    Timestamp:   time.Now().UnixMilli(),
                })
                return
            }
            
            // Send output to client
            session.Send(WebSocketMessage{
                Type:        "output",
                ExecutionID: req.ExecutionID,
                Stream:      output.Stream,
                Content:     output.Content,
                Timestamp:   time.Now().UnixMilli(),
            })
        }
        
        // Notify client that execution has completed
        session.Send(WebSocketMessage{
            Type:        "execution_complete",
            ExecutionID: req.ExecutionID,
            ExitCode:    0, // Get actual exit code from final stream message
            Timestamp:   time.Now().UnixMilli(),
        })
    }()
}
```

#### 2.4 File Service

The File Service handles file operations:

```go
// FileService handles file operations
type FileService struct {
    k8sClient kubernetes.Interface
}

// UploadFile uploads a file to a sandbox
func (s *FileService) UploadFile(ctx context.Context, req UploadFileRequest) (*FileInfo, error) {
    // Get sandbox
    sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Get(ctx, req.SandboxID, metav1.GetOptions{})
    if err != nil {
        return nil, err
    }
    
    // Check if sandbox is running
    if sandbox.Status.Phase != "Running" {
        return nil, errors.New("sandbox is not running")
    }
    
    // Create file upload request
    fileReq := &v1.FileRequest{
        Path:    req.Path,
        Content: req.Content,
    }
    
    // Upload file via Kubernetes API
    fileResult, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).UploadFile(ctx, req.SandboxID, fileReq)
    if err != nil {
        return nil, err
    }
    
    // Return file info
    return &FileInfo{
        Path:      fileResult.Path,
        Size:      fileResult.Size,
        CreatedAt: fileResult.CreatedAt.Time,
    }, nil
}

// DownloadFile downloads a file from a sandbox
func (s *FileService) DownloadFile(ctx context.Context, req DownloadFileRequest) ([]byte, error) {
    // Get sandbox
    sandbox, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).Get(ctx, req.SandboxID, metav1.GetOptions{})
    if err != nil {
        return nil, err
    }
    
    // Check if sandbox is running
    if sandbox.Status.Phase != "Running" {
        return nil, errors.New("sandbox is not running")
    }
    
    // Create file download request
    fileReq := &v1.FileRequest{
        Path: req.Path,
    }
    
    // Download file via Kubernetes API
    fileResult, err := s.k8sClient.LlmsafespaceV1().Sandboxes(req.Namespace).DownloadFile(ctx, req.SandboxID, fileReq)
    if err != nil {
        return nil, err
    }
    
    return fileResult.Content, nil
}
```

### 3. Integration Layer

#### 3.1 Kubernetes Client

The Kubernetes Client handles communication with the Kubernetes API:

```go
// K8sClient manages Kubernetes API interactions
type K8sClient struct {
    clientset       kubernetes.Interface
    dynamicClient   dynamic.Interface
    restConfig      *rest.Config
    informerFactory informers.SharedInformerFactory
}

// NewK8sClient creates a new Kubernetes client
func NewK8sClient(kubeconfig string) (*K8sClient, error) {
    // Load Kubernetes configuration
    config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
    if err != nil {
        return nil, err
    }
    
    // Create clientset
    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        return nil, err
    }
    
    // Create dynamic client
    dynamicClient, err := dynamic.NewForConfig(config)
    if err != nil {
        return nil, err
    }
    
    // Create informer factory
    informerFactory := informers.NewSharedInformerFactory(clientset, 30*time.Second)
    
    return &K8sClient{
        clientset:       clientset,
        dynamicClient:   dynamicClient,
        restConfig:      config,
        informerFactory: informerFactory,
    }, nil
}

// Start starts the informer factory
func (c *K8sClient) Start(stopCh <-chan struct{}) {
    c.informerFactory.Start(stopCh)
    c.informerFactory.WaitForCacheSync(stopCh)
}
```

#### 3.2 Database Client

The Database Client handles communication with PostgreSQL:

```go
// DatabaseClient manages database interactions
type DatabaseClient struct {
    db *sql.DB
}

// NewDatabaseClient creates a new database client
func NewDatabaseClient(connString string) (*DatabaseClient, error) {
    // Connect to database
    db, err := sql.Open("postgres", connString)
    if err != nil {
        return nil, err
    }
    
    // Configure connection pool
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(25)
    db.SetConnMaxLifetime(5 * time.Minute)
    
    // Test connection
    if err := db.Ping(); err != nil {
        return nil, err
    }
    
    return &DatabaseClient{db: db}, nil
}

// CreateSandboxMetadata stores sandbox metadata in the database
func (c *DatabaseClient) CreateSandboxMetadata(ctx context.Context, meta *SandboxMetadata) error {
    query := `
        INSERT INTO sandboxes (id, user_id, created_at, runtime)
        VALUES ($1, $2, $3, $4)
    `
    
    _, err := c.db.ExecContext(ctx, query, meta.ID, meta.UserID, meta.CreatedAt, meta.Runtime)
    return err
}
```

#### 3.3 Cache Client

The Cache Client handles communication with Redis:

```go
// CacheClient manages cache interactions
type CacheClient struct {
    client *redis.Client
}

// NewCacheClient creates a new cache client
func NewCacheClient(addr, password string, db int) (*CacheClient, error) {
    // Create Redis client
    client := redis.NewClient(&redis.Options{
        Addr:     addr,
        Password: password,
        DB:       db,
    })
    
    // Test connection
    if err := client.Ping().Err(); err != nil {
        return nil, err
    }
    
    return &CacheClient{client: client}, nil
}

// Get retrieves a value from the cache
func (c *CacheClient) Get(ctx context.Context, key string) (string, error) {
    return c.client.Get(ctx, key).Result()
}

// SetWithTTL stores a value in the cache with a TTL
func (c *CacheClient) SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error {
    return c.client.Set(ctx, key, value, ttl).Err()
}

// Delete removes a value from the cache
func (c *CacheClient) Delete(ctx context.Context, key string) error {
    return c.client.Del(ctx, key).Err()
}
```

## Session Management

### WebSocket Session Management

The Session Manager handles WebSocket connections and message routing:

```go
// Session represents a WebSocket session
type Session struct {
    ID            string
    UserID        string
    conn          *websocket.Conn
    send          chan WebSocketMessage
    cancellations map[string]context.CancelFunc
    mu            sync.Mutex
}

// SessionManager manages WebSocket sessions
type SessionManager struct {
    sessions map[string]*Session
    mu       sync.RWMutex
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
    return &SessionManager{
        sessions: make(map[string]*Session),
    }
}

// CreateSession creates a new session
func (m *SessionManager) CreateSession(userID string, conn *websocket.Conn) *Session {
    session := &Session{
        ID:            uuid.New().String(),
        UserID:        userID,
        conn:          conn,
        send:          make(chan WebSocketMessage, 256),
        cancellations: make(map[string]context.CancelFunc),
    }
    
    m.mu.Lock()
    m.sessions[session.ID] = session
    m.mu.Unlock()
    
    // Start goroutines for reading and writing
    go session.writePump()
    
    return session
}

// CloseSession closes a session
func (m *SessionManager) CloseSession(sessionID string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    if session, ok := m.sessions[sessionID]; ok {
        // Cancel all pending executions
        session.mu.Lock()
        for _, cancel := range session.cancellations {
            cancel()
        }
        session.mu.Unlock()
        
        // Close send channel
        close(session.send)
        
        // Remove from sessions map
        delete(m.sessions, sessionID)
    }
}

// Send sends a message to the session
func (s *Session) Send(msg WebSocketMessage) {
    select {
    case s.send <- msg:
        // Message sent to channel
    default:
        // Channel is full, log error
    }
}

// SetCancellationFunc sets a cancellation function for an execution
func (s *Session) SetCancellationFunc(executionID string, cancel context.CancelFunc) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.cancellations[executionID] = cancel
}

// CancelExecution cancels an execution
func (s *Session) CancelExecution(executionID string) bool {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if cancel, ok := s.cancellations[executionID]; ok {
        cancel()
        delete(s.cancellations, executionID)
        return true
    }
    
    return false
}

// writePump pumps messages from the send channel to the WebSocket connection
func (s *Session) writePump() {
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        s.conn.Close()
    }()
    
    for {
        select {
        case message, ok := <-s.send:
            s.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if !ok {
                // Channel was closed
                s.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            
            // Write message as JSON
            if err := s.conn.WriteJSON(message); err != nil {
                return
            }
        case <-ticker.C:
            s.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                return
            }
        }
    }
}
```

## Error Handling

### Standardized Error Handling

The API Service implements standardized error handling:

```go
// ErrorResponse represents a standardized error response
type ErrorResponse struct {
    Error ErrorDetails `json:"error"`
}

// ErrorDetails contains detailed error information
type ErrorDetails struct {
    Code    string                 `json:"code"`
    Message string                 `json:"message"`
    Details map[string]interface{} `json:"details,omitempty"`
}

// NewErrorResponse creates a new error response
func NewErrorResponse(code, message string, details map[string]interface{}) ErrorResponse {
    return ErrorResponse{
        Error: ErrorDetails{
            Code:    code,
            Message: message,
            Details: details,
        },
    }
}

// HandleError handles an error and returns an appropriate HTTP response
func HandleError(c *gin.Context, err error) {
    // Check for specific error types
    switch e := err.(type) {
    case *ValidationError:
        c.JSON(400, NewErrorResponse("invalid_request", e.Message, e.Details))
    case *AuthError:
        c.JSON(401, NewErrorResponse("unauthorized", e.Message, nil))
    case *ForbiddenError:
        c.JSON(403, NewErrorResponse("forbidden", e.Message, nil))
    case *NotFoundError:
        c.JSON(404, NewErrorResponse("not_found", e.Message, nil))
    case *ConflictError:
        c.JSON(409, NewErrorResponse("conflict", e.Message, nil))
    case *RateLimitError:
        c.Header("X-RateLimit-Limit", strconv.Itoa(e.Limit))
        c.Header("X-RateLimit-Remaining", "0")
        c.Header("X-RateLimit-Reset", strconv.FormatInt(e.Reset, 10))
        c.JSON(429, NewErrorResponse("rate_limited", e.Message, nil))
    default:
        // Log unexpected errors
        log.Printf("Unexpected error: %v", err)
        c.JSON(500, NewErrorResponse("internal_error", "An unexpected error occurred", nil))
    }
}
```

## Logging

### Structured Logging

The API Service uses structured logging for better observability:

```go
// Logger provides structured logging
type Logger struct {
    logger *zap.Logger
}

// NewLogger creates a new logger
func NewLogger(development bool) (*Logger, error) {
    var logger *zap.Logger
    var err error
    
    if development {
        logger, err = zap.NewDevelopment()
    } else {
        config := zap.NewProductionConfig()
        config.EncoderConfig.TimeKey = "timestamp"
        config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
        logger, err = config.Build()
    }
    
    if err != nil {
        return nil, err
    }
    
    return &Logger{logger: logger}, nil
}

// Info logs an info message
func (l *Logger) Info(msg string, fields ...zap.Field) {
    l.logger.Info(msg, fields...)
}

// Error logs an error message
func (l *Logger) Error(msg string, err error, fields ...zap.Field) {
    fields = append(fields, zap.Error(err))
    l.logger.Error(msg, fields...)
}

// With returns a logger with additional fields
func (l *Logger) With(fields ...zap.Field) *Logger {
    return &Logger{logger: l.logger.With(fields...)}
}
```

## Rate Limiting

### Rate Limiter Implementation

The API Service implements rate limiting:

```go
// RateLimiter manages rate limiting
type RateLimiter struct {
    store  *redis.Client
    limits map[string]RateLimit
}

// RateLimit defines a rate limit
type RateLimit struct {
    Requests int
    Window   time.Duration
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(store *redis.Client) *RateLimiter {
    return &RateLimiter{
        store: store,
        limits: map[string]RateLimit{
            "default":       {1000, time.Hour},
            "create_sandbox": {100, time.Hour},
            "execute_code":   {500, time.Hour},
        },
    }
}

// RateLimitMiddleware returns a middleware function for rate limiting
func (r *RateLimiter) RateLimitMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // Get API key from context
        apiKey := c.GetString("apiKey")
        if apiKey == "" {
            c.Next()
            return
        }
        
        // Determine limit type based on endpoint
        limitType := "default"
        path := c.FullPath()
        method := c.Request.Method
        
        if method == "POST" && strings.HasSuffix(path, "/sandboxes") {
            limitType = "create_sandbox"
        } else if method == "POST" && strings.Contains(path, "/execute") {
            limitType = "execute_code"
        }
        
        // Get limit for this type
        limit, ok := r.limits[limitType]
        if !ok {
            limit = r.limits["default"]
        }
        
        // Check rate limit
        key := fmt.Sprintf("ratelimit:%s:%s", apiKey, limitType)
        count, err := r.store.Incr(c, key).Result()
        if err != nil {
            // Log error but continue
            c.Next()
            return
        }
        
        // Set expiry on first request
        if count == 1 {
            r.store.Expire(c, key, limit.Window)
        }
        
        // Set rate limit headers
        c.Header("X-RateLimit-Limit", strconv.Itoa(limit.Requests))
        c.Header("X-RateLimit-Remaining", strconv.Itoa(limit.Requests-int(count)))
        
        // Get TTL for reset time
        ttl, err := r.store.TTL(c, key).Result()
        if err == nil {
            resetTime := time.Now().Add(ttl).Unix()
            c.Header("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))
        }
        
        // Check if limit exceeded
        if count > int64(limit.Requests) {
            c.AbortWithStatusJSON(429, NewErrorResponse(
                "rate_limited",
                fmt.Sprintf("Rate limit exceeded. Try again in %v", ttl),
                nil,
            ))
            return
        }
        
        c.Next()
    }
}
```

## Metrics and Monitoring

### Prometheus Metrics

The API Service exposes Prometheus metrics:

```go
// MetricsService manages application metrics
type MetricsService struct {
    requestCounter   *prometheus.CounterVec
    requestDuration  *prometheus.HistogramVec
    responseSize     *prometheus.HistogramVec
    activeConnections *prometheus.GaugeVec
    warmPoolHitRatio *prometheus.GaugeVec
}

// NewMetricsService creates a new metrics service
func NewMetricsService() *MetricsService {
    requestCounter := prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "api_requests_total",
            Help: "Total number of API requests",
        },
        []string{"method", "endpoint", "status"},
    )
    
    requestDuration := prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "api_request_duration_seconds",
            Help:    "API request duration in seconds",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "endpoint"},
    )
    
    responseSize := prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "api_response_size_bytes",
            Help:    "API response size in bytes",
            Buckets: prometheus.ExponentialBuckets(100, 10, 8),
        },
        []string{"method", "endpoint"},
    )
    
    activeConnections := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "api_active_connections",
            Help: "Number of active connections",
        },
        []string{"type"},
    )
    
    warmPoolHitRatio := prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "warm_pool_hit_ratio",
            Help: "Ratio of sandbox creations that used a warm pod",
        },
        []string{"runtime"},
    )
    
    // Register metrics
    prometheus.MustRegister(requestCounter)
    prometheus.MustRegister(requestDuration)
    prometheus.MustRegister(responseSize)
    prometheus.MustRegister(activeConnections)
    prometheus.MustRegister(warmPoolHitRatio)
    
    return &MetricsService{
        requestCounter:   requestCounter,
        requestDuration:  requestDuration,
        responseSize:     responseSize,
        activeConnections: activeConnections,
        warmPoolHitRatio: warmPoolHitRatio,
    }
}

// MetricsMiddleware returns a middleware function for collecting metrics
func (m *MetricsService) MetricsMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        
        // Process request
        c.Next()
        
        // Record metrics
        duration := time.Since(start).Seconds()
        status := strconv.Itoa(c.Writer.Status())
        endpoint := c.FullPath()
        method := c.Request.Method
        
        m.requestCounter.WithLabelValues(method, endpoint, status).Inc()
        m.requestDuration.WithLabelValues(method, endpoint).Observe(duration)
        m.responseSize.WithLabelValues(method, endpoint).Observe(float64(c.Writer.Size()))
    }
}
```

## Configuration

### Configuration Management

The API Service uses a structured configuration system:

```go
// Config represents the application configuration
type Config struct {
    Server struct {
        Host string `yaml:"host"`
        Port int    `yaml:"port"`
    } `yaml:"server"`
    
    Kubernetes struct {
        ConfigPath string `yaml:"configPath"`
        InCluster  bool   `yaml:"inCluster"`
    } `yaml:"kubernetes"`
    
    Database struct {
        Host     string `yaml:"host"`
        Port     int    `yaml:"port"`
        User     string `yaml:"user"`
        Password string `yaml:"password"`
        Database string `yaml:"database"`
        SSLMode  string `yaml:"sslMode"`
    } `yaml:"database"`
    
    Redis struct {
        Host     string `yaml:"host"`
        Port     int    `yaml:"port"`
        Password string `yaml:"password"`
        DB       int    `yaml:"db"`
    } `yaml:"redis"`
    
    Auth struct {
        JWTSecret     string        `yaml:"jwtSecret"`
        TokenDuration time.Duration `yaml:"tokenDuration"`
    } `yaml:"auth"`
    
    Logging struct {
        Level       string `yaml:"level"`
        Development bool   `yaml:"development"`
    } `yaml:"logging"`
    
    RateLimiting struct {
        Enabled bool `yaml:"enabled"`
        Limits  map[string]struct {
            Requests int           `yaml:"requests"`
            Window   time.Duration `yaml:"window"`
        } `yaml:"limits"`
    } `yaml:"rateLimiting"`
}

// LoadConfig loads configuration from file and environment variables
func LoadConfig(path string) (*Config, error) {
    var config Config
    
    // Read config file
    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }
    
    // Parse YAML
    if err := yaml.Unmarshal(data, &config); err != nil {
        return nil, err
    }
    
    // Override with environment variables
    if host := os.Getenv("SERVER_HOST"); host != "" {
        config.Server.Host = host
    }
    
    if portStr := os.Getenv("SERVER_PORT"); portStr != "" {
        if port, err := strconv.Atoi(portStr); err == nil {
            config.Server.Port = port
        }
    }
    
    // Similar overrides for other config values...
    
    return &config, nil
}
```

## Main Application

### Application Initialization

The main application ties everything together:

```go
// App represents the main application
type App struct {
    config         *Config
    router         *gin.Engine
    k8sClient      *K8sClient
    databaseClient *DatabaseClient
    cacheClient    *CacheClient
    logger         *Logger
    metrics        *MetricsService
    
    sandboxService    *SandboxService
    warmPoolService   *WarmPoolService
    executionService  *ExecutionService
    fileService       *FileService
    authService       *AuthService
    sessionManager    *SessionManager
}

// NewApp creates a new application
func NewApp(config *Config) (*App, error) {
    // Initialize logger
    logger, err := NewLogger(config.Logging.Development)
    if err != nil {
        return nil, err
    }
    
    // Initialize Kubernetes client
    var k8sClient *K8sClient
    if config.Kubernetes.InCluster {
        k8sClient, err = NewK8sClientInCluster()
    } else {
        k8sClient, err = NewK8sClient(config.Kubernetes.ConfigPath)
    }
    if err != nil {
        return nil, err
    }
    
    // Initialize database client
    dbConnString := fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
        config.Database.Host,
        config.Database.Port,
        config.Database.User,
        config.Database.Password,
        config.Database.Database,
        config.Database.SSLMode,
    )
    databaseClient, err := NewDatabaseClient(dbConnString)
    if err != nil {
        return nil, err
    }
    
    // Initialize cache client
    cacheClient, err := NewCacheClient(
        fmt.Sprintf("%s:%d", config.Redis.Host, config.Redis.Port),
        config.Redis.Password,
        config.Redis.DB,
    )
    if err != nil {
        return nil, err
    }
    
    // Initialize metrics
    metrics := NewMetricsService()
    
    // Initialize session manager
    sessionManager := NewSessionManager()
    
    // Initialize services
    authService := NewAuthService(databaseClient, config.Auth.JWTSecret, config.Auth.TokenDuration)
    fileService := NewFileService(k8sClient)
    executionService := NewExecutionService(k8sClient, sessionManager)
    warmPoolService := NewWarmPoolService(k8sClient, databaseClient, cacheClient)
    sandboxService := NewSandboxService(k8sClient, warmPoolService, databaseClient, cacheClient, executionService, fileService)
    
    // Initialize router
    router := gin.New()
    router.Use(gin.Recovery())
    router.Use(metrics.MetricsMiddleware())
    
    // Configure rate limiting if enabled
    if config.RateLimiting.Enabled {
        rateLimiter := NewRateLimiter(cacheClient.client)
        router.Use(rateLimiter.RateLimitMiddleware())
    }
    
    return &App{
        config:         config,
        router:         router,
        k8sClient:      k8sClient,
        databaseClient: databaseClient,
        cacheClient:    cacheClient,
        logger:         logger,
        metrics:        metrics,
        
        sandboxService:   sandboxService,
        warmPoolService:  warmPoolService,
        executionService: executionService,
        fileService:      fileService,
        authService:      authService,
        sessionManager:   sessionManager,
    }, nil
}

// SetupRoutes sets up the API routes
func (a *App) SetupRoutes() {
    // API version group
    v1 := a.router.Group("/api/v1")
    v1.Use(a.authService.AuthMiddleware())
    
    // Sandbox routes
    sandboxHandler := NewSandboxHandler(a.sandboxService, a.authService)
    sandboxHandler.Routes(v1)
    
    // Warm pool routes
    warmPoolHandler := NewWarmPoolHandler(a.warmPoolService, a.authService)
    warmPoolHandler.Routes(v1)
    
    // Runtime routes
    runtimeHandler := NewRuntimeHandler(a.k8sClient)
    runtimeHandler.Routes(v1)
    
    // Profile routes
    profileHandler := NewProfileHandler(a.k8sClient)
    profileHandler.Routes(v1)
    
    // User routes
    userHandler := NewUserHandler(a.authService)
    userHandler.Routes(v1)
    
    // WebSocket route
    v1.GET("/sandboxes/:id/stream", a.handleWebSocket)
    
    // Metrics endpoint
    a.router.GET("/metrics", gin.WrapH(promhttp.Handler()))
    
    // Health check
    a.router.GET("/health", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })
}

// handleWebSocket handles WebSocket connections
func (a *App) handleWebSocket(c *gin.Context) {
    // Get sandbox ID
    sandboxID := c.Param("id")
    
    // Authenticate user
    userID, err := a.authService.AuthenticateWebSocket(c)
    if err != nil {
        c.JSON(401, NewErrorResponse("unauthorized", "Invalid or expired token", nil))
        return
    }
    
    // Check if user has access to this sandbox
    if !a.authService.CheckResourceAccess(userID, "sandbox", sandboxID, "connect") {
        c.JSON(403, NewErrorResponse("forbidden", "You don't have access to this sandbox", nil))
        return
    }
    
    // Upgrade connection to WebSocket
    upgrader := websocket.Upgrader{
        ReadBufferSize:  1024,
        WriteBufferSize: 1024,
        CheckOrigin: func(r *http.Request) bool {
            // In production, implement proper origin checking
            return true
        },
    }
    
    conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        a.logger.Error("Failed to upgrade connection", err)
        return
    }
    
    // Create session
    session := a.sessionManager.CreateSession(userID, conn)
    defer a.sessionManager.CloseSession(session.ID)
    
    // Increment active connections metric
    a.metrics.activeConnections.WithLabelValues("websocket").Inc()
    defer a.metrics.activeConnections.WithLabelValues("websocket").Dec()
    
    // Handle WebSocket messages
    for {
        messageType, p, err := conn.ReadMessage()
        if err != nil {
            break
        }
        
        // Only handle text messages
        if messageType != websocket.TextMessage {
            continue
        }
        
        // Parse message
        var message map[string]interface{}
        if err := json.Unmarshal(p, &message); err != nil {
            session.Send(WebSocketMessage{
                Type:    "error",
                Code:    "invalid_message",
                Message: "Invalid message format",
            })
            continue
        }
        
        // Handle message based on type
        msgType, ok := message["type"].(string)
        if !ok {
            session.Send(WebSocketMessage{
                Type:    "error",
                Code:    "invalid_message",
                Message: "Missing message type",
            })
            continue
        }
        
        switch msgType {
        case "execute":
            // Handle execution request
            executionID, _ := message["executionId"].(string)
            mode, _ := message["mode"].(string)
            content, _ := message["content"].(string)
            timeout, _ := message["timeout"].(float64)
            
            if executionID == "" || (mode != "code" && mode != "command") || content == "" {
                session.Send(WebSocketMessage{
                    Type:    "error",
                    Code:    "invalid_request",
                    Message: "Invalid execution request",
                })
                continue
            }
            
            // Execute code/command
            a.executionService.StreamExecution(session, StreamExecutionRequest{
                SandboxID:   sandboxID,
                Namespace:   "default", // Get from sandbox
                ExecutionID: executionID,
                Mode:        mode,
                Content:     content,
                Timeout:     int(timeout),
            })
            
        case "cancel":
            // Handle cancellation request
            executionID, _ := message["executionId"].(string)
            if executionID == "" {
                session.Send(WebSocketMessage{
                    Type:    "error",
                    Code:    "invalid_request",
                    Message: "Missing executionId",
                })
                continue
            }
            
            // Cancel execution
            if cancelled := session.CancelExecution(executionID); cancelled {
                session.Send(WebSocketMessage{
                    Type:        "execution_cancelled",
                    ExecutionID: executionID,
                    Timestamp:   time.Now().UnixMilli(),
                })
            }
            
        case "ping":
            // Handle ping
            session.Send(WebSocketMessage{
                Type:      "pong",
                Timestamp: time.Now().UnixMilli(),
            })
            
        default:
            session.Send(WebSocketMessage{
                Type:    "error",
                Code:    "unknown_message_type",
                Message: "Unknown message type",
            })
        }
    }
}

// Run starts the application
func (a *App) Run() error {
    // Start Kubernetes client
    stopCh := make(chan struct{})
    defer close(stopCh)
    a.k8sClient.Start(stopCh)
    
    // Start HTTP server
    addr := fmt.Sprintf("%s:%d", a.config.Server.Host, a.config.Server.Port)
    a.logger.Info("Starting server", zap.String("address", addr))
    
    srv := &http.Server{
        Addr:    addr,
        Handler: a.router,
    }
    
    // Graceful shutdown
    go func() {
        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
        <-sigCh
        
        a.logger.Info("Shutting down server")
        
        // Create shutdown context with timeout
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        
        if err := srv.Shutdown(ctx); err != nil {
            a.logger.Error("Server shutdown error", err)
        }
    }()
    
    // Start server
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        return err
    }
    
    return nil
}
```

## Deployment

### Kubernetes Deployment

The API Service is deployed as a Kubernetes Deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: agent-api
  namespace: llmsafespace
  labels:
    app: llmsafespace
    component: api
spec:
  replicas: 3
  selector:
    matchLabels:
      app: llmsafespace
      component: api
  template:
    metadata:
      labels:
        app: llmsafespace
        component: api
    spec:
      serviceAccountName: agent-api
      containers:
      - name: api
        image: llmsafespace/agent-api:latest
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: SERVER_PORT
          value: "8080"
        - name: KUBERNETES_IN_CLUSTER
          value: "true"
        - name: DATABASE_HOST
          value: "postgres"
        - name: DATABASE_PORT
          value: "5432"
        - name: DATABASE_USER
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: username
        - name: DATABASE_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: password
        - name: DATABASE_NAME
          value: "llmsafespace"
        - name: REDIS_HOST
          value: "redis"
        - name: REDIS_PORT
          value: "6379"
        - name: AUTH_JWT_SECRET
          valueFrom:
            secretKeyRef:
              name: api-secrets
              key: jwt-secret
        resources:
          requests:
            cpu: 100m
            memory: 256Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /health
            port: http
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: http
          initialDelaySeconds: 5
          periodSeconds: 5
        volumeMounts:
        - name: config
          mountPath: /app/config
          readOnly: true
      volumes:
      - name: config
        configMap:
          name: agent-api-config
```

### Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: agent-api
  namespace: llmsafespace
  labels:
    app: llmsafespace
    component: api
spec:
  selector:
    app: llmsafespace
    component: api
  ports:
  - port: 80
    targetPort: http
    name: http
  type: ClusterIP
```

### Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: agent-api
  namespace: llmsafespace
  annotations:
    kubernetes.io/ingress.class: nginx
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
    nginx.ingress.kubernetes.io/proxy-body-size: "10m"
spec:
  rules:
  - host: api.llmsafespace.dev
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: agent-api
            port:
              name: http
  tls:
  - hosts:
    - api.llmsafespace.dev
    secretName: llmsafespace-tls
```

## Conclusion

This low-level design document provides a comprehensive blueprint for implementing the API Service component of LLMSafeSpace. The design follows best practices for building scalable, secure, and maintainable API services, with a focus on:

1. **Clean Architecture**: Clear separation of concerns with API, service, and integration layers
2. **Security**: Robust authentication, authorization, and input validation
3. **Performance**: Efficient resource usage, connection pooling, and caching
4. **Observability**: Comprehensive logging, metrics, and health checks
5. **Scalability**: Stateless design for horizontal scaling
6. **Reliability**: Graceful error handling and shutdown procedures

The API Service integrates seamlessly with the Sandbox Controller through Kubernetes custom resources, providing a unified platform for secure code execution in isolated environments. The warm pool coordination ensures efficient resource usage and fast sandbox creation times, enhancing the overall user experience.
