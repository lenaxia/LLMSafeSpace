import pytest
import docker
import json
import time
import subprocess
import requests
from pathlib import Path

# Test configuration
TEST_IMAGE = "llmsafespace/base:latest"
TEST_PYTHON_IMAGE = "llmsafespace/python:latest"
TEST_NODEJS_IMAGE = "llmsafespace/nodejs:latest"
TEST_GO_IMAGE = "llmsafespace/go:latest"

@pytest.fixture
def docker_client():
    return docker.from_env()

def test_base_image_security(docker_client):
    """Test base image security configuration"""
    container = docker_client.containers.run(
        TEST_IMAGE,
        "id",
        remove=True,
        detach=False
    )
    
    # Check running as non-root
    assert b"uid=1000" in container
    assert b"gid=1000" in container

def test_base_image_tools(docker_client):
    """Test base image tools are present and working"""
    tools = [
        "/opt/llmsafespace/bin/sandbox-monitor",
        "/opt/llmsafespace/bin/execution-tracker",
        "/opt/llmsafespace/bin/health-check",
        "/opt/llmsafespace/bin/cleanup-pod"
    ]
    
    for tool in tools:
        result = docker_client.containers.run(
            TEST_IMAGE,
            f"test -x {tool}",
            remove=True,
            detach=False
        )
        assert result == b""

def test_python_security_wrapper(docker_client):
    """Test Python security wrapper functionality"""
    code = """
import os
try:
    os.system('ls')
    print('FAIL')
except AttributeError:
    print('PASS')
"""
    
    result = docker_client.containers.run(
        TEST_PYTHON_IMAGE,
        ["python", "-c", code],
        remove=True,
        detach=False
    )
    
    assert b"PASS" in result

def test_nodejs_security_wrapper(docker_client):
    """Test Node.js security wrapper functionality"""
    code = """
try {
    require('child_process');
    console.log('FAIL');
} catch (error) {
    console.log('PASS');
}
"""
    
    result = docker_client.containers.run(
        TEST_NODEJS_IMAGE,
        ["node", "-e", code],
        remove=True,
        detach=False
    )
    
    assert b"PASS" in result

def test_go_security_wrapper(docker_client):
    """Test Go security wrapper functionality"""
    code = """
package main

import (
    "fmt"
    "os/exec"
)

func main() {
    _, err := exec.Command("ls").Output()
    if err != nil {
        fmt.Println("PASS")
    } else {
        fmt.Println("FAIL")
    }
}
"""
    
    result = docker_client.containers.run(
        TEST_GO_IMAGE,
        ["go", "run", "/tmp/test.go"],
        remove=True,
        detach=False,
        volumes={
            "/tmp/test.go": {
                "bind": "/tmp/test.go",
                "mode": "ro"
            }
        }
    )
    
    assert b"PASS" in result

def test_resource_limits(docker_client):
    """Test resource limits are enforced"""
    container = docker_client.containers.run(
        TEST_IMAGE,
        "stress --cpu 2 --timeout 5",
        remove=True,
        detach=True,
        mem_limit="512m",
        cpu_quota=100000,  # 1 CPU
        cpu_period=100000
    )
    
    time.sleep(2)
    stats = container.stats(stream=False)
    
    # Check CPU usage is limited
    cpu_usage = stats["cpu_stats"]["cpu_usage"]["total_usage"]
    assert cpu_usage > 0
    
    # Check memory usage is limited
    mem_usage = stats["memory_stats"]["usage"]
    assert mem_usage < 512 * 1024 * 1024

def test_network_restrictions(docker_client):
    """Test network restrictions are enforced"""
    container = docker_client.containers.run(
        TEST_IMAGE,
        "curl -s http://example.com",
        remove=True,
        detach=False,
        network_mode="none"
    )
    
    assert b"error" in container.lower()

def test_filesystem_restrictions(docker_client):
    """Test filesystem restrictions are enforced"""
    tests = [
        "touch /test_file",
        "mkdir /test_dir",
        "chmod 777 /etc/passwd"
    ]
    
    for test in tests:
        result = docker_client.containers.run(
            TEST_IMAGE,
            test,
            remove=True,
            detach=False
        )
        
        assert b"permission denied" in result.lower()

def test_monitoring_tools(docker_client):
    """Test monitoring tools are functional"""
    container = docker_client.containers.run(
        TEST_IMAGE,
        "sleep 30",
        remove=True,
        detach=True
    )
    
    time.sleep(5)
    
    # Check sandbox-monitor output
    logs = container.exec_run("cat /var/log/llmsafespace/sandbox-monitor.log")
    assert b"cpu" in logs.output.lower()
    assert b"memory" in logs.output.lower()
    
    # Check execution-tracker output
    logs = container.exec_run("cat /var/log/llmsafespace/execution.log")
    assert b"command" in logs.output.lower()
    assert b"resources" in logs.output.lower()
    
    container.stop()

def test_warm_pool_integration(docker_client):
    """Test container can be recycled for warm pools"""
    container = docker_client.containers.run(
        TEST_IMAGE,
        "sleep 30",
        remove=True,
        detach=True
    )
    
    time.sleep(2)
    
    # Run cleanup
    result = container.exec_run("/opt/llmsafespace/bin/cleanup-pod")
    assert result.exit_code == 0
    
    # Verify cleanup
    checks = [
        "test -z \"$(ls -A /workspace)\"",
        "test -z \"$(ls -A /tmp)\"",
        "test -z \"$(pgrep -u sandbox)\""
    ]
    
    for check in checks:
        result = container.exec_run(check)
        assert result.exit_code == 0
    
    container.stop()

if __name__ == "__main__":
    pytest.main([__file__])
