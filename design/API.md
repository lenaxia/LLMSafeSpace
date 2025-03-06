# API Design for LLMSafeSpace

## Overview

The LLMSafeSpace API is designed to provide a secure, scalable interface for managing sandbox environments and executing code. The API consists of:

1. **REST API** - For resource management and control operations
2. **WebSocket API** - For real-time streaming of execution outputs
3. **SDK Interfaces** - Language-specific client libraries that abstract the API

## REST API

### Base URL

```
https://{api-host}/api/v1
```

### Authentication

All API requests require authentication using one of:

- API Key (via `Authorization: Bearer {api-key}` header)
- OAuth 2.0 tokens (for integration with identity providers)

### Resources

#### Sandboxes

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/sandboxes` | GET | List all sandboxes for the authenticated user |
| `/sandboxes` | POST | Create a new sandbox |
| `/sandboxes/{id}` | GET | Get sandbox details |
| `/sandboxes/{id}` | DELETE | Terminate a sandbox |
| `/sandboxes/{id}/status` | GET | Get sandbox status |
| `/sandboxes/{id}/execute` | POST | Execute code or command |
| `/sandboxes/{id}/files` | GET | List files in sandbox |
| `/sandboxes/{id}/files/{path}` | GET | Download a file |
| `/sandboxes/{id}/files/{path}` | PUT | Upload a file |
| `/sandboxes/{id}/files/{path}` | DELETE | Delete a file |
| `/sandboxes/{id}/packages` | POST | Install packages |

#### Runtime Environments

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/runtimes` | GET | List available runtime environments |
| `/runtimes/{id}` | GET | Get runtime details |

#### Security Profiles

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/profiles` | GET | List available security profiles |
| `/profiles/{id}` | GET | Get profile details |

#### Warm Pools

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/warmpools` | GET | List all warm pools |
| `/warmpools` | POST | Create a new warm pool |
| `/warmpools/{id}` | GET | Get warm pool details |
| `/warmpools/{id}` | PATCH | Update a warm pool |
| `/warmpools/{id}` | DELETE | Delete a warm pool |
| `/warmpools/{id}/status` | GET | Get warm pool status |

#### User Management

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/user` | GET | Get current user info |
| `/user/apikeys` | GET | List API keys |
| `/user/apikeys` | POST | Create new API key |
| `/user/apikeys/{id}` | DELETE | Revoke API key |

### Request/Response Examples

#### Create Sandbox

**Request:**
```json
POST /api/v1/sandboxes
{
  "runtime": "python:3.10",
  "securityLevel": "standard",
  "timeout": 300,
  "resources": {
    "cpu": "1",
    "memory": "1Gi"
  },
  "networkAccess": {
    "egress": [
      {"domain": "pypi.org"},
      {"domain": "files.pythonhosted.org"}
    ]
  },
  "useWarmPool": true
}
```

**Response:**
```json
{
  "id": "sb-a1b2c3d4",
  "status": "creating",
  "createdAt": "2023-07-01T10:00:00Z",
  "runtime": "python:3.10",
  "securityLevel": "standard",
  "timeout": 300,
  "resources": {
    "cpu": "1",
    "memory": "1Gi"
  },
  "networkAccess": {
    "egress": [
      {"domain": "pypi.org"},
      {"domain": "files.pythonhosted.org"}
    ]
  },
  "endpoints": {
    "execute": "/api/v1/sandboxes/sb-a1b2c3d4/execute",
    "files": "/api/v1/sandboxes/sb-a1b2c3d4/files",
    "websocket": "wss://api-host/api/v1/sandboxes/sb-a1b2c3d4/stream"
  }
}
```

#### Execute Code

**Request:**
```json
POST /api/v1/sandboxes/sb-a1b2c3d4/execute
{
  "type": "code",
  "language": "python",
  "content": "import numpy as np\nprint(np.random.rand(5,5))",
  "timeout": 30
}
```

**Response:**
```json
{
  "executionId": "exec-e1f2g3h4",
  "status": "completed",
  "startedAt": "2023-07-01T10:05:00Z",
  "completedAt": "2023-07-01T10:05:01Z",
  "exitCode": 0,
  "stdout": "[[0.81 0.44 0.77 0.45 0.25]\n [0.14 0.61 0.86 0.37 0.07]\n [0.11 0.29 0.69 0.59 0.18]\n [0.93 0.25 0.24 0.95 0.24]\n [0.35 0.11 0.34 0.48 0.88]]\n",
  "stderr": ""
}
```

#### Execute Command

**Request:**
```json
POST /api/v1/sandboxes/sb-a1b2c3d4/execute
{
  "type": "command",
  "content": "ls -la /workspace",
  "timeout": 10
}
```

**Response:**
```json
{
  "executionId": "exec-i5j6k7l8",
  "status": "completed",
  "startedAt": "2023-07-01T10:10:00Z",
  "completedAt": "2023-07-01T10:10:01Z",
  "exitCode": 0,
  "stdout": "total 8\ndrwxr-xr-x 2 user user 4096 Jul  1 10:00 .\ndrwxr-xr-x 1 user user 4096 Jul  1 10:00 ..\n",
  "stderr": ""
}
```

#### Upload File

**Request:**
```
PUT /api/v1/sandboxes/sb-a1b2c3d4/files/data.csv
Content-Type: application/octet-stream

[Binary file content]
```

**Response:**
```json
{
  "path": "data.csv",
  "size": 1024,
  "createdAt": "2023-07-01T10:15:00Z"
}
```

#### Install Packages

**Request:**
```json
POST /api/v1/sandboxes/sb-a1b2c3d4/packages
{
  "packages": ["pandas", "matplotlib"],
  "manager": "pip"
}
```

**Response:**
```json
{
  "status": "completed",
  "startedAt": "2023-07-01T10:20:00Z",
  "completedAt": "2023-07-01T10:20:30Z",
  "exitCode": 0,
  "stdout": "Successfully installed pandas-2.0.1 matplotlib-3.7.1",
  "stderr": ""
}
```

#### Create Warm Pool

**Request:**
```json
POST /api/v1/warmpools
{
  "name": "python-pool",
  "runtime": "python:3.10",
  "minSize": 5,
  "maxSize": 20,
  "securityLevel": "standard",
  "preloadPackages": ["numpy", "pandas"],
  "autoScaling": {
    "enabled": true,
    "targetUtilization": 80,
    "scaleDownDelay": 300
  }
}
```

**Response:**
```json
{
  "id": "wp-a1b2c3d4",
  "name": "python-pool",
  "runtime": "python:3.10",
  "minSize": 5,
  "maxSize": 20,
  "securityLevel": "standard",
  "preloadPackages": ["numpy", "pandas"],
  "autoScaling": {
    "enabled": true,
    "targetUtilization": 80,
    "scaleDownDelay": 300
  },
  "status": {
    "availablePods": 0,
    "assignedPods": 0,
    "pendingPods": 5,
    "lastScaleTime": "2023-07-01T10:00:00Z"
  },
  "createdAt": "2023-07-01T10:00:00Z"
}
```

#### Get Warm Pool Status

**Request:**
```
GET /api/v1/warmpools/wp-a1b2c3d4/status
```

**Response:**
```json
{
  "availablePods": 5,
  "assignedPods": 2,
  "pendingPods": 0,
  "lastScaleTime": "2023-07-01T10:05:00Z",
  "conditions": [
    {
      "type": "Ready",
      "status": "True",
      "reason": "PoolReady",
      "message": "Warm pool is ready",
      "lastTransitionTime": "2023-07-01T10:05:00Z"
    }
  ]
}
```

## WebSocket API

The WebSocket API provides real-time streaming of execution outputs and interactive sessions.

### Connection

```
wss://{api-host}/api/v1/sandboxes/{id}/stream
```

Authentication is handled via a query parameter or header:
```
wss://{api-host}/api/v1/sandboxes/{id}/stream?token={api-key}
```

### Message Format

All WebSocket messages are JSON objects with a `type` field that indicates the message type.

#### Client Messages

**Execute Code:**
```json
{
  "type": "execute",
  "executionId": "client-gen-id-123",
  "mode": "code",
  "language": "python",
  "content": "import time\nfor i in range(5):\n    print(f'Count: {i}')\n    time.sleep(1)",
  "timeout": 30
}
```

**Execute Command:**
```json
{
  "type": "execute",
  "executionId": "client-gen-id-456",
  "mode": "command",
  "content": "find /workspace -type f | wc -l",
  "timeout": 10
}
```

**Cancel Execution:**
```json
{
  "type": "cancel",
  "executionId": "client-gen-id-123"
}
```

**Heartbeat:**
```json
{
  "type": "ping",
  "timestamp": 1625140800000
}
```

#### Server Messages

**Execution Start:**
```json
{
  "type": "execution_start",
  "executionId": "client-gen-id-123",
  "timestamp": 1625140800000
}
```

**Execution Output:**
```json
{
  "type": "output",
  "executionId": "client-gen-id-123",
  "stream": "stdout",
  "content": "Count: 0\n",
  "timestamp": 1625140801000
}
```

**Execution Complete:**
```json
{
  "type": "execution_complete",
  "executionId": "client-gen-id-123",
  "exitCode": 0,
  "timestamp": 1625140805000
}
```

**Error:**
```json
{
  "type": "error",
  "code": "execution_timeout",
  "message": "Execution timed out after 30 seconds",
  "executionId": "client-gen-id-123",
  "timestamp": 1625140830000
}
```

**Heartbeat Response:**
```json
{
  "type": "pong",
  "timestamp": 1625140800100
}
```

**Sandbox Status Update:**
```json
{
  "type": "status_update",
  "status": "running",
  "resources": {
    "cpuUsage": 0.45,
    "memoryUsage": "256Mi"
  },
  "timestamp": 1625140810000
}
```

## SDK Interfaces

The SDKs provide language-specific abstractions over the REST and WebSocket APIs. Here are the core interfaces for the Python, JavaScript, and Go SDKs.

### Python SDK

```python
class Sandbox:
    def __init__(
        self, 
        runtime: str, 
        api_key: str = None, 
        security_level: str = "standard", 
        timeout: int = 300,
        resources: dict = None,
        network_access: dict = None,
        api_url: str = None,
        use_warm_pool: bool = True
    ):
        """Initialize a new sandbox or connect to an existing one."""
        pass
    
    def run_code(self, code: str, timeout: int = None) -> ExecutionResult:
        """Execute code and return the result."""
        pass
    
    def run_command(self, command: str, timeout: int = None) -> ExecutionResult:
        """Execute a shell command and return the result."""
        pass
    
    def stream_code(self, code: str, timeout: int = None) -> Iterator[str]:
        """Execute code and stream the output."""
        pass
    
    def stream_command(self, command: str, timeout: int = None) -> Iterator[str]:
        """Execute a shell command and stream the output."""
        pass
    
    def upload_file(self, remote_path: str, local_path: str = None, content: bytes = None) -> FileInfo:
        """Upload a file to the sandbox."""
        pass
    
    def download_file(self, remote_path: str, local_path: str = None) -> Union[bytes, str]:
        """Download a file from the sandbox."""
        pass
    
    def list_files(self, path: str = "/workspace") -> List[FileInfo]:
        """List files in the sandbox."""
        pass
    
    def install(self, packages: Union[str, List[str]], manager: str = None) -> ExecutionResult:
        """Install packages in the sandbox."""
        pass
    
    def terminate(self) -> bool:
        """Terminate the sandbox."""
        pass
    
    def status(self) -> SandboxStatus:
        """Get the current status of the sandbox."""
        pass
    
    @property
    def id(self) -> str:
        """Get the sandbox ID."""
        pass
    
    @property
    def runtime(self) -> str:
        """Get the sandbox runtime."""
        pass
    
    @property
    def created_at(self) -> datetime:
        """Get the sandbox creation time."""
        pass


class ExecutionResult:
    """Result of code or command execution."""
    
    @property
    def stdout(self) -> str:
        """Standard output from the execution."""
        pass
    
    @property
    def stderr(self) -> str:
        """Standard error from the execution."""
        pass
    
    @property
    def exit_code(self) -> int:
        """Exit code of the execution."""
        pass
    
    @property
    def execution_time(self) -> float:
        """Execution time in seconds."""
        pass
    
    @property
    def success(self) -> bool:
        """Whether the execution was successful (exit code 0)."""
        pass


class FileInfo:
    """Information about a file in the sandbox."""
    
    @property
    def path(self) -> str:
        """Path of the file relative to the sandbox root."""
        pass
    
    @property
    def size(self) -> int:
        """Size of the file in bytes."""
        pass
    
    @property
    def created_at(self) -> datetime:
        """Creation time of the file."""
        pass
    
    @property
    def modified_at(self) -> datetime:
        """Last modification time of the file."""
        pass
    
    @property
    def is_directory(self) -> bool:
        """Whether the file is a directory."""
        pass


class SandboxStatus:
    """Status of a sandbox."""
    
    @property
    def state(self) -> str:
        """Current state of the sandbox (creating, running, terminated, etc.)."""
        pass
    
    @property
    def resources(self) -> dict:
        """Current resource usage of the sandbox."""
        pass
    
    @property
    def uptime(self) -> float:
        """Uptime of the sandbox in seconds."""
        pass


class WarmPool:
    """Represents a warm pool of sandbox environments."""
    
    def __init__(
        self, 
        name: str,
        runtime: str,
        min_size: int = 1,
        max_size: int = 10,
        security_level: str = "standard",
        preload_packages: List[str] = None,
        auto_scaling: bool = False,
        api_key: str = None,
        api_url: str = None
    ):
        """Initialize a new warm pool."""
        pass
    
    @property
    def name(self) -> str:
        """Get the warm pool name."""
        pass
    
    @property
    def runtime(self) -> str:
        """Get the runtime environment."""
        pass
    
    @property
    def min_size(self) -> int:
        """Get the minimum pool size."""
        pass
    
    @property
    def max_size(self) -> int:
        """Get the maximum pool size."""
        pass
    
    @property
    def available_pods(self) -> int:
        """Get the number of available pods."""
        pass
    
    @property
    def assigned_pods(self) -> int:
        """Get the number of assigned pods."""
        pass
    
    def scale(self, min_size: int = None, max_size: int = None) -> 'WarmPool':
        """Scale the warm pool."""
        pass
    
    def delete(self) -> bool:
        """Delete the warm pool."""
        pass
    
    def status(self) -> dict:
        """Get the current status of the warm pool."""
        pass


def list_warm_pools(api_key: str = None, api_url: str = None) -> List[WarmPool]:
    """List all warm pools."""
    pass

def get_warm_pool(name: str, api_key: str = None, api_url: str = None) -> WarmPool:
    """Get a warm pool by name."""
    pass
```

### Python SDK Implementation

```python
import os
import json
import time
import uuid
import requests
import websocket
from typing import Dict, List, Union, Iterator, Optional
from datetime import datetime

class ExecutionResult:
    def __init__(self, data: Dict):
        self._data = data
    
    @property
    def stdout(self) -> str:
        return self._data.get("stdout", "")
    
    @property
    def stderr(self) -> str:
        return self._data.get("stderr", "")
    
    @property
    def exit_code(self) -> int:
        return self._data.get("exitCode", 0)
    
    @property
    def execution_time(self) -> float:
        started = self._data.get("startedAt")
        completed = self._data.get("completedAt")
        if started and completed:
            start_time = datetime.fromisoformat(started.replace("Z", "+00:00"))
            end_time = datetime.fromisoformat(completed.replace("Z", "+00:00"))
            return (end_time - start_time).total_seconds()
        return 0.0
    
    @property
    def success(self) -> bool:
        return self.exit_code == 0


class FileInfo:
    def __init__(self, data: Dict):
        self._data = data
    
    @property
    def path(self) -> str:
        return self._data.get("path", "")
    
    @property
    def size(self) -> int:
        return self._data.get("size", 0)
    
    @property
    def created_at(self) -> datetime:
        created = self._data.get("createdAt")
        if created:
            return datetime.fromisoformat(created.replace("Z", "+00:00"))
        return datetime.now()
    
    @property
    def modified_at(self) -> datetime:
        modified = self._data.get("modifiedAt")
        if modified:
            return datetime.fromisoformat(modified.replace("Z", "+00:00"))
        return self.created_at
    
    @property
    def is_directory(self) -> bool:
        return self._data.get("isDirectory", False)


class SandboxStatus:
    def __init__(self, data: Dict):
        self._data = data
    
    @property
    def state(self) -> str:
        return self._data.get("status", "unknown")
    
    @property
    def resources(self) -> dict:
        return self._data.get("resources", {})
    
    @property
    def uptime(self) -> float:
        created = self._data.get("createdAt")
        if created:
            start_time = datetime.fromisoformat(created.replace("Z", "+00:00"))
            return (datetime.now() - start_time).total_seconds()
        return 0.0


class Sandbox:
    def __init__(
        self, 
        runtime: str = None, 
        sandbox_id: str = None,
        api_key: str = None, 
        security_level: str = "standard", 
        timeout: int = 300,
        resources: dict = None,
        network_access: dict = None,
        api_url: str = None
    ):
        """
        Initialize a new sandbox or connect to an existing one.
        
        Args:
            runtime: The runtime environment (e.g., "python:3.10")
            sandbox_id: ID of an existing sandbox to connect to
            api_key: API key for authentication (defaults to LLMSAFESPACE_API_KEY env var)
            security_level: Security level ("standard", "high", or "custom")
            timeout: Default timeout for operations in seconds
            resources: Resource limits (e.g., {"cpu": "1", "memory": "1Gi"})
            network_access: Network access rules
            api_url: API endpoint URL (defaults to LLMSAFESPACE_API_URL env var)
        """
        self._api_key = api_key or os.environ.get("LLMSAFESPACE_API_KEY")
        if not self._api_key:
            raise ValueError("API key is required. Set it in the constructor or LLMSAFESPACE_API_KEY env var.")
        
        self._api_url = api_url or os.environ.get("LLMSAFESPACE_API_URL", "https://api.llmsafespace.dev/api/v1")
        self._timeout = timeout
        self._sandbox_data = None
        
        if sandbox_id:
            # Connect to existing sandbox
            self._sandbox_id = sandbox_id
            self._load_sandbox_data()
        elif runtime:
            # Create new sandbox
            payload = {
                "runtime": runtime,
                "securityLevel": security_level,
                "timeout": timeout
            }
            
            if resources:
                payload["resources"] = resources
                
            if network_access:
                payload["networkAccess"] = network_access
                
            response = self._make_request("POST", "/sandboxes", json=payload)
            self._sandbox_data = response
            self._sandbox_id = response["id"]
        else:
            raise ValueError("Either runtime or sandbox_id must be provided")
    
    def _make_request(self, method, path, **kwargs):
        """Make an HTTP request to the API."""
        url = f"{self._api_url}{path}"
        headers = {
            "Authorization": f"Bearer {self._api_key}",
            "Content-Type": "application/json"
        }
        
        response = requests.request(
            method, 
            url, 
            headers=headers, 
            timeout=kwargs.pop("timeout", self._timeout),
            **kwargs
        )
        
        if response.status_code >= 400:
            try:
                error_data = response.json()
                error_msg = error_data.get("error", {}).get("message", "Unknown error")
                error_code = error_data.get("error", {}).get("code", "unknown_error")
                raise Exception(f"API error ({error_code}): {error_msg}")
            except json.JSONDecodeError:
                raise Exception(f"API error: {response.status_code} {response.text}")
        
        return response.json()
    
    def _load_sandbox_data(self):
        """Load sandbox data from the API."""
        self._sandbox_data = self._make_request("GET", f"/sandboxes/{self._sandbox_id}")
    
    def run_code(self, code: str, timeout: int = None) -> ExecutionResult:
        """
        Execute code and return the result.
        
        Args:
            code: The code to execute
            timeout: Execution timeout in seconds (overrides default)
            
        Returns:
            ExecutionResult object with stdout, stderr, and exit code
        """
        payload = {
            "type": "code",
            "language": self.runtime.split(":")[0],
            "content": code,
            "timeout": timeout or self._timeout
        }
        
        response = self._make_request(
            "POST", 
            f"/sandboxes/{self._sandbox_id}/execute",
            json=payload
        )
        
        return ExecutionResult(response)
    
    def run_command(self, command: str, timeout: int = None) -> ExecutionResult:
        """
        Execute a shell command and return the result.
        
        Args:
            command: The shell command to execute
            timeout: Execution timeout in seconds (overrides default)
            
        Returns:
            ExecutionResult object with stdout, stderr, and exit code
        """
        payload = {
            "type": "command",
            "content": command,
            "timeout": timeout or self._timeout
        }
        
        response = self._make_request(
            "POST", 
            f"/sandboxes/{self._sandbox_id}/execute",
            json=payload
        )
        
        return ExecutionResult(response)
    
    def stream_code(self, code: str, timeout: int = None) -> Iterator[str]:
        """
        Execute code and stream the output.
        
        Args:
            code: The code to execute
            timeout: Execution timeout in seconds (overrides default)
            
        Returns:
            Iterator yielding output lines as they become available
        """
        ws_url = f"{self._api_url.replace('http', 'ws')}/sandboxes/{self._sandbox_id}/stream?token={self._api_key}"
        execution_id = str(uuid.uuid4())
        
        ws = websocket.create_connection(ws_url)
        
        try:
            # Send execution request
            ws.send(json.dumps({
                "type": "execute",
                "executionId": execution_id,
                "mode": "code",
                "language": self.runtime.split(":")[0],
                "content": code,
                "timeout": timeout or self._timeout
            }))
            
            # Process messages until execution completes
            while True:
                message = json.loads(ws.recv())
                
                if message["type"] == "output" and message["executionId"] == execution_id:
                    yield message["content"]
                
                elif message["type"] == "execution_complete" and message["executionId"] == execution_id:
                    break
                
                elif message["type"] == "error" and message["executionId"] == execution_id:
                    raise Exception(f"Execution error: {message['message']}")
        
        finally:
            ws.close()
    
    def stream_command(self, command: str, timeout: int = None) -> Iterator[str]:
        """
        Execute a shell command and stream the output.
        
        Args:
            command: The shell command to execute
            timeout: Execution timeout in seconds (overrides default)
            
        Returns:
            Iterator yielding output lines as they become available
        """
        ws_url = f"{self._api_url.replace('http', 'ws')}/sandboxes/{self._sandbox_id}/stream?token={self._api_key}"
        execution_id = str(uuid.uuid4())
        
        ws = websocket.create_connection(ws_url)
        
        try:
            # Send execution request
            ws.send(json.dumps({
                "type": "execute",
                "executionId": execution_id,
                "mode": "command",
                "content": command,
                "timeout": timeout or self._timeout
            }))
            
            # Process messages until execution completes
            while True:
                message = json.loads(ws.recv())
                
                if message["type"] == "output" and message["executionId"] == execution_id:
                    yield message["content"]
                
                elif message["type"] == "execution_complete" and message["executionId"] == execution_id:
                    break
                
                elif message["type"] == "error" and message["executionId"] == execution_id:
                    raise Exception(f"Execution error: {message['message']}")
        
        finally:
            ws.close()
    
    def upload_file(self, remote_path: str, local_path: str = None, content: bytes = None) -> FileInfo:
        """
        Upload a file to the sandbox.
        
        Args:
            remote_path: Path in the sandbox where the file will be stored
            local_path: Local file path to upload (mutually exclusive with content)
            content: File content as bytes (mutually exclusive with local_path)
            
        Returns:
            FileInfo object with metadata about the uploaded file
        """
        if local_path and content:
            raise ValueError("Only one of local_path or content should be provided")
        
        if local_path:
            with open(local_path, "rb") as f:
                content = f.read()
        elif content is None:
            raise ValueError("Either local_path or content must be provided")
        
        headers = {
            "Authorization": f"Bearer {self._api_key}",
            "Content-Type": "application/octet-stream"
        }
        
        url = f"{self._api_url}/sandboxes/{self._sandbox_id}/files/{remote_path}"
        response = requests.put(url, headers=headers, data=content, timeout=self._timeout)
        
        if response.status_code >= 400:
            try:
                error_data = response.json()
                error_msg = error_data.get("error", {}).get("message", "Unknown error")
                raise Exception(f"File upload error: {error_msg}")
            except json.JSONDecodeError:
                raise Exception(f"File upload error: {response.status_code} {response.text}")
        
        return FileInfo(response.json())
    
    def download_file(self, remote_path: str, local_path: str = None) -> Union[bytes, str]:
        """
        Download a file from the sandbox.
        
        Args:
            remote_path: Path in the sandbox to the file to download
            local_path: Local path where the file will be saved (optional)
            
        Returns:
            File content as bytes if local_path is None, otherwise None
        """
        response = requests.get(
            f"{self._api_url}/sandboxes/{self._sandbox_id}/files/{remote_path}",
            headers={"Authorization": f"Bearer {self._api_key}"},
            timeout=self._timeout
        )
        
        if response.status_code >= 400:
            try:
                error_data = response.json()
                error_msg = error_data.get("error", {}).get("message", "Unknown error")
                raise Exception(f"File download error: {error_msg}")
            except json.JSONDecodeError:
                raise Exception(f"File download error: {response.status_code} {response.text}")
        
        if local_path:
            with open(local_path, "wb") as f:
                f.write(response.content)
            return None
        else:
            return response.content
    
    def list_files(self, path: str = "/workspace") -> List[FileInfo]:
        """
        List files in the sandbox.
        
        Args:
            path: Directory path to list
            
        Returns:
            List of FileInfo objects
        """
        response = self._make_request("GET", f"/sandboxes/{self._sandbox_id}/files?path={path}")
        return [FileInfo(item) for item in response.get("files", [])]
    
    def install(self, packages: Union[str, List[str]], manager: str = None) -> ExecutionResult:
        """
        Install packages in the sandbox.
        
        Args:
            packages: Package name(s) to install (string or list of strings)
            manager: Package manager to use (defaults to auto-detect based on runtime)
            
        Returns:
            ExecutionResult object with installation output
        """
        if isinstance(packages, str):
            packages = packages.split()
        
        payload = {
            "packages": packages
        }
        
        if manager:
            payload["manager"] = manager
        
        response = self._make_request(
            "POST", 
            f"/sandboxes/{self._sandbox_id}/packages",
            json=payload
        )
        
        return ExecutionResult(response)
    
    def terminate(self) -> bool:
        """
        Terminate the sandbox.
        
        Returns:
            True if successful
        """
        self._make_request("DELETE", f"/sandboxes/{self._sandbox_id}")
        return True
    
    def status(self) -> SandboxStatus:
        """
        Get the current status of the sandbox.
        
        Returns:
            SandboxStatus object with current state and resource usage
        """
        response = self._make_request("GET", f"/sandboxes/{self._sandbox_id}/status")
        return SandboxStatus(response)
    
    @property
    def id(self) -> str:
        """Get the sandbox ID."""
        return self._sandbox_id
    
    @property
    def runtime(self) -> str:
        """Get the sandbox runtime."""
        return self._sandbox_data.get("runtime", "")
    
    @property
    def created_at(self) -> datetime:
        """Get the sandbox creation time."""
        created = self._sandbox_data.get("createdAt")
        if created:
            return datetime.fromisoformat(created.replace("Z", "+00:00"))
        return datetime.now()


# Helper function to connect to an existing sandbox
def connect(sandbox_id: str, api_key: str = None, api_url: str = None) -> Sandbox:
    """
    Connect to an existing sandbox by ID.
    
    Args:
        sandbox_id: ID of the sandbox to connect to
        api_key: API key for authentication (defaults to LLMSAFESPACE_API_KEY env var)
        api_url: API endpoint URL (defaults to LLMSAFESPACE_API_URL env var)
        
    Returns:
        Sandbox object connected to the existing sandbox
    """
    return Sandbox(
        sandbox_id=sandbox_id,
        api_key=api_key,
        api_url=api_url
    )
```

### JavaScript/TypeScript SDK

```typescript
interface SandboxOptions {
  runtime: string;
  apiKey?: string;
  securityLevel?: 'standard' | 'high' | 'custom';
  timeout?: number;
  resources?: {
    cpu?: string;
    memory?: string;
  };
  networkAccess?: {
    egress?: Array<{ domain: string }>;
    ingress?: boolean;
  };
  apiUrl?: string;
  useWarmPool?: boolean;
}

interface ExecutionResult {
  stdout: string;
  stderr: string;
  exitCode: number;
  executionTime: number;
  success: boolean;
}

interface FileInfo {
  path: string;
  size: number;
  createdAt: Date;
  modifiedAt: Date;
  isDirectory: boolean;
}

interface SandboxStatus {
  state: 'creating' | 'running' | 'terminated' | 'error';
  resources: {
    cpuUsage: number;
    memoryUsage: string;
  };
  uptime: number;
}

class Sandbox {
  constructor(options: SandboxOptions);
  
  // Properties
  readonly id: string;
  readonly runtime: string;
  readonly createdAt: Date;
  
  // Methods
  async runCode(code: string, timeout?: number): Promise<ExecutionResult>;
  async runCommand(command: string, timeout?: number): Promise<ExecutionResult>;
  
  streamCode(code: string, timeout?: number): EventEmitter;
  streamCommand(command: string, timeout?: number): EventEmitter;
  
  async uploadFile(remotePath: string, options: { localPath?: string, content?: Buffer | string }): Promise<FileInfo>;
  async downloadFile(remotePath: string, localPath?: string): Promise<Buffer | void>;
  async listFiles(path?: string): Promise<FileInfo[]>;
  
  async install(packages: string | string[], manager?: string): Promise<ExecutionResult>;
  async terminate(): Promise<boolean>;
  async status(): Promise<SandboxStatus>;
}

interface WarmPoolOptions {
  name: string;
  runtime: string;
  minSize?: number;
  maxSize?: number;
  securityLevel?: 'standard' | 'high' | 'custom';
  preloadPackages?: string[];
  autoScaling?: {
    enabled: boolean;
    targetUtilization?: number;
    scaleDownDelay?: number;
  };
  apiKey?: string;
  apiUrl?: string;
}

interface WarmPoolStatus {
  availablePods: number;
  assignedPods: number;
  pendingPods: number;
  lastScaleTime: Date;
  conditions: Array<{
    type: string;
    status: 'True' | 'False' | 'Unknown';
    reason: string;
    message: string;
    lastTransitionTime: Date;
  }>;
}

class WarmPool {
  constructor(options: WarmPoolOptions);
  
  // Properties
  readonly name: string;
  readonly runtime: string;
  readonly minSize: number;
  readonly maxSize: number;
  readonly availablePods: number;
  readonly assignedPods: number;
  
  // Methods
  async scale(options: { minSize?: number, maxSize?: number }): Promise<WarmPool>;
  async delete(): Promise<boolean>;
  async status(): Promise<WarmPoolStatus>;
}

// Helper functions
async function listWarmPools(options?: { apiKey?: string, apiUrl?: string }): Promise<WarmPool[]>;
async function getWarmPool(name: string, options?: { apiKey?: string, apiUrl?: string }): Promise<WarmPool>;
```

### Go SDK

```go
package llmsafespace

import (
	"context"
	"io"
	"time"
)

// SandboxOptions defines options for creating a new sandbox
type SandboxOptions struct {
	Runtime       string
	APIKey        string
	SecurityLevel string
	Timeout       int
	Resources     *ResourceOptions
	NetworkAccess *NetworkOptions
	APIURL        string
	UseWarmPool   bool
}

// ResourceOptions defines resource limits for a sandbox
type ResourceOptions struct {
	CPU    string
	Memory string
}

// NetworkOptions defines network access rules for a sandbox
type NetworkOptions struct {
	Egress  []DomainRule
	Ingress bool
}

// DomainRule defines a domain that can be accessed
type DomainRule struct {
	Domain string
}

// ExecutionResult contains the result of code or command execution
type ExecutionResult struct {
	Stdout        string
	Stderr        string
	ExitCode      int
	ExecutionTime float64
	Success       bool
}

// FileInfo contains information about a file in the sandbox
type FileInfo struct {
	Path        string
	Size        int64
	CreatedAt   time.Time
	ModifiedAt  time.Time
	IsDirectory bool
}

// SandboxStatus contains the current status of a sandbox
type SandboxStatus struct {
	State    string
	Resources struct {
		CPUUsage    float64
		MemoryUsage string
	}
	Uptime float64
}

// Sandbox represents a secure execution environment
type Sandbox interface {
	// Properties
	ID() string
	Runtime() string
	CreatedAt() time.Time

	// Execution methods
	RunCode(ctx context.Context, code string, timeout ...int) (*ExecutionResult, error)
	RunCommand(ctx context.Context, command string, timeout ...int) (*ExecutionResult, error)
	
	StreamCode(ctx context.Context, code string, output io.Writer, timeout ...int) error
	StreamCommand(ctx context.Context, command string, output io.Writer, timeout ...int) error
	
	// File operations
	UploadFile(ctx context.Context, remotePath string, content io.Reader) (*FileInfo, error)
	UploadFileFromPath(ctx context.Context, remotePath, localPath string) (*FileInfo, error)
	DownloadFile(ctx context.Context, remotePath string) (io.ReadCloser, error)
	DownloadFileToPath(ctx context.Context, remotePath, localPath string) error
	ListFiles(ctx context.Context, path ...string) ([]FileInfo, error)
	
	// Package management
	Install(ctx context.Context, packages []string, manager ...string) (*ExecutionResult, error)
	
	// Lifecycle management
	Terminate(ctx context.Context) (bool, error)
	Status(ctx context.Context) (*SandboxStatus, error)
}

// New creates a new sandbox with the given options
func New(opts SandboxOptions) (Sandbox, error) {
	// Implementation
}

// Connect connects to an existing sandbox by ID
func Connect(id string, apiKey string) (Sandbox, error) {
	// Implementation
}

// WarmPoolOptions defines options for creating a new warm pool
type WarmPoolOptions struct {
	Name           string
	Runtime        string
	MinSize        int
	MaxSize        int
	SecurityLevel  string
	PreloadPackages []string
	AutoScaling    *AutoScalingOptions
	APIKey         string
	APIURL         string
}

// AutoScalingOptions defines auto-scaling configuration for a warm pool
type AutoScalingOptions struct {
	Enabled          bool
	TargetUtilization int
	ScaleDownDelay   int
}

// WarmPoolStatus contains the current status of a warm pool
type WarmPoolStatus struct {
	AvailablePods  int
	AssignedPods   int
	PendingPods    int
	LastScaleTime  time.Time
	Conditions     []WarmPoolCondition
}

// WarmPoolCondition represents a condition of a warm pool
type WarmPoolCondition struct {
	Type               string
	Status             string
	Reason             string
	Message            string
	LastTransitionTime time.Time
}

// WarmPool represents a pool of pre-initialized sandbox environments
type WarmPool interface {
	// Properties
	Name() string
	Runtime() string
	MinSize() int
	MaxSize() int
	AvailablePods() int
	AssignedPods() int
	
	// Methods
	Scale(ctx context.Context, minSize, maxSize int) error
	Delete(ctx context.Context) error
	Status(ctx context.Context) (*WarmPoolStatus, error)
}

// NewWarmPool creates a new warm pool with the given options
func NewWarmPool(opts WarmPoolOptions) (WarmPool, error) {
	// Implementation
}

// GetWarmPool gets an existing warm pool by name
func GetWarmPool(name string, apiKey string) (WarmPool, error) {
	// Implementation
}

// ListWarmPools lists all warm pools
func ListWarmPools(apiKey string) ([]WarmPool, error) {
	// Implementation
}
```

## Error Handling

### Error Codes

The API uses standard HTTP status codes along with specific error codes in the response body:

| HTTP Status | Error Code | Description |
|-------------|------------|-------------|
| 400 | invalid_request | The request was malformed |
| 401 | unauthorized | Authentication failed |
| 403 | forbidden | Permission denied |
| 404 | not_found | Resource not found |
| 409 | conflict | Resource conflict |
| 429 | rate_limited | Too many requests |
| 500 | internal_error | Internal server error |
| 503 | service_unavailable | Service unavailable |

### Error Response Format

```json
{
  "error": {
    "code": "execution_failed",
    "message": "Execution failed with exit code 1",
    "details": {
      "exitCode": 1,
      "stderr": "NameError: name 'undefined_variable' is not defined"
    }
  }
}
```

## Rate Limiting

The API implements rate limiting to prevent abuse:

- Rate limits are applied per API key
- Limits are defined for different endpoints (higher limits for execution, lower for creation)
- Rate limit headers are included in responses:
  - `X-RateLimit-Limit`: Total requests allowed in the current period
  - `X-RateLimit-Remaining`: Requests remaining in the current period
  - `X-RateLimit-Reset`: Time when the rate limit resets (Unix timestamp)

## Versioning

The API is versioned via the URL path (`/api/v1/`). Breaking changes will be introduced in new API versions while maintaining backward compatibility for at least one version.
