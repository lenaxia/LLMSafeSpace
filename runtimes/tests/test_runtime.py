import pytest
import docker
import json
import time
import subprocess
import requests
from pathlib import Path

# Test configuration
TEST_IMAGE = "ghcr.io/lenaxia/llmsafespace/base:latest"
TEST_PYTHON_IMAGE = "ghcr.io/lenaxia/llmsafespace/python:latest"
TEST_NODEJS_IMAGE = "ghcr.io/lenaxia/llmsafespace/nodejs:latest"
TEST_GO_IMAGE = "ghcr.io/lenaxia/llmsafespace/go:latest"

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
        ["/opt/llmsafespace/bin/python-security-wrapper.py", "-c", code],
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
        ["/opt/llmsafespace/bin/nodejs-security-wrapper.js", "-e", code],
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
    
    import tempfile
    
    # Create a temporary file
    with tempfile.NamedTemporaryFile(suffix='.go', mode='w', delete=False) as f:
        f.write(code)
        temp_go_file = f.name
    
    result = docker_client.containers.run(
        TEST_GO_IMAGE,
        ["/opt/llmsafespace/bin/go-security-wrapper", "/tmp/test.go"],
        remove=True,
        detach=False,
        volumes={
            temp_go_file: {
                "bind": "/workspace/test.go",
                "mode": "ro"
            }
        }
    )
    
    assert b"PASS" in result

def test_resource_limits(docker_client):
    """Test resource limits are enforced"""
    # Install stress package first
    setup = docker_client.containers.run(
        TEST_IMAGE,
        ["apt-get", "update", "&&", "apt-get", "install", "-y", "stress"],
        remove=True,
        user="root"
    )
    
    container = docker_client.containers.run(
        TEST_IMAGE,
        ["stress", "--cpu", "2", "--timeout", "5"],
        remove=True,
        detach=True,
        mem_limit="512m",
        cpu_quota=100000,  # 1 CPU
        cpu_period=100000,
        user="root"  # Needed to run stress
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
    try:
        docker_client.containers.run(
            TEST_IMAGE,
            ["ping", "-c", "1", "8.8.8.8"],
            remove=True,
            detach=False,
            network_mode="none"
        )
        assert False, "Network request should have failed"
    except docker.errors.ContainerError as e:
        assert b"network is unreachable" in e.stderr.lower() or b"error" in e.stderr.lower() or b"network is down" in e.stderr.lower()

def test_filesystem_restrictions(docker_client):
    """Test filesystem restrictions are enforced"""
    tests = [
        ["touch", "/test_file"],
        ["mkdir", "/test_dir"],
        ["chmod", "777", "/etc/passwd"]
    ]
    
    for test in tests:
        try:
            docker_client.containers.run(
                TEST_IMAGE,
                test,
                remove=True,
                detach=False,
                read_only=True  # Ensure root filesystem is read-only
            )
            assert False, f"Command {test} should have failed"
        except docker.errors.ContainerError as e:
            assert b"permission denied" in e.stderr.lower() or b"read-only" in e.stderr.lower()

def test_monitoring_tools(docker_client):
    """Test monitoring tools are functional"""
    # Create log directory
    import tempfile
    log_dir = tempfile.mkdtemp()
    
    # Start monitoring tools
    container = docker_client.containers.run(
        TEST_IMAGE,
        ["/bin/bash", "-c", "mkdir -p /var/log/llmsafespace && /opt/llmsafespace/bin/sandbox-monitor & /opt/llmsafespace/bin/execution-tracker & sleep 30"],
        remove=True,
        detach=True,
        volumes={
            log_dir: {
                "bind": "/var/log/llmsafespace",
                "mode": "rw"
            }
        }
    )
    
    time.sleep(10)  # Give more time for logs to be generated
    
    try:
        # Check sandbox-monitor output
        logs = container.exec_run("cat /var/log/llmsafespace/sandbox-monitor.log")
        assert b"cpu" in logs.output.lower() or b"memory" in logs.output.lower(), "Missing monitoring data"
        
        # Check execution-tracker output
        logs = container.exec_run("cat /var/log/llmsafespace/execution.log")
        assert b"command" in logs.output.lower() or b"resources" in logs.output.lower(), "Missing execution data"
    finally:
        container.stop()

def test_warm_pool_integration(docker_client):
    """Test container can be recycled for warm pools"""
    try:
        container = docker_client.containers.run(
            TEST_IMAGE,
            ["sleep", "30"],
            remove=True,
            detach=True,
            volumes={
                "/workspace": {"bind": "/workspace", "mode": "rw"},
                "/tmp": {"bind": "/tmp", "mode": "rw"}
            }
        )
        
        # Create some test files
        container.exec_run("touch /workspace/test1.txt")
        container.exec_run("touch /tmp/test2.txt")
        
        time.sleep(2)
        
        # Run cleanup
        result = container.exec_run("/opt/llmsafespace/bin/cleanup-pod")
        assert result.exit_code == 0, f"Cleanup failed: {result.output}"
        
        # Verify cleanup
        for path in ["/workspace", "/tmp"]:
            result = container.exec_run(f"find {path} -mindepth 1")
            assert not result.output.strip(), f"Directory {path} not empty after cleanup"
        
        # Check no user processes
        result = container.exec_run("pgrep -u sandbox || true")
        assert not result.output.strip(), "User processes still running after cleanup"
        
    finally:
        container.stop()

if __name__ == "__main__":
    pytest.main([__file__])
