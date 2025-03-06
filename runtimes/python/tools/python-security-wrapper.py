#!/usr/bin/env python3
import os
import sys
import json
import resource
import importlib.util
import subprocess

# Load restricted modules configuration
with open('/etc/llmsafespace/python/restricted_modules.json', 'r') as f:
    RESTRICTED_MODULES = json.load(f)

# Set resource limits
def set_resource_limits():
    # CPU time limit (seconds)
    resource.setrlimit(resource.RLIMIT_CPU, (300, 300))
    # Virtual memory limit (bytes) - 1GB
    resource.setrlimit(resource.RLIMIT_AS, (1024 * 1024 * 1024, 1024 * 1024 * 1024))
    # File size limit (bytes) - 100MB
    resource.setrlimit(resource.RLIMIT_FSIZE, (100 * 1024 * 1024, 100 * 1024 * 1024))

# Custom import hook to restrict dangerous modules
class RestrictedImportFinder:
    def __init__(self, restricted_modules):
        self.restricted_modules = restricted_modules
    
    def find_spec(self, fullname, path, target=None):
        if fullname in self.restricted_modules['blocked']:
            raise ImportError(f"Import of '{fullname}' is not allowed for security reasons")
        
        if fullname in self.restricted_modules['warning']:
            print(f"WARNING: Importing '{fullname}' may pose security risks", file=sys.stderr)
        
        return None

# Register the import hook
sys.meta_path.insert(0, RestrictedImportFinder(RESTRICTED_MODULES))

# Set resource limits
set_resource_limits()

# Execute the Python interpreter with the provided script
if __name__ == "__main__":
    if len(sys.argv) > 1:
        script_path = sys.argv[1]
        sys.argv = sys.argv[1:]
        
        with open(script_path, 'rb') as f:
            code = compile(f.read(), script_path, 'exec')
            exec(code, {'__name__': '__main__'})
    else:
        # Interactive mode
        import code
        code.interact(banner="LLMSafeSpace Python Environment", exitmsg="")
