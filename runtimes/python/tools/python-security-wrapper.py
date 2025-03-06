#!/usr/bin/env python3
import os
import sys
import json
import resource
import importlib.util

# Load restricted modules configuration
with open('/etc/llmsafespace/python/restricted_modules.json', 'r') as f:
    RESTRICTED_MODULES = json.load(f)

# Set resource limits
def set_resource_limits():
    try:
        # CPU time limit (seconds)
        resource.setrlimit(resource.RLIMIT_CPU, (300, 300))
        # Virtual memory limit (bytes) - 1GB
        resource.setrlimit(resource.RLIMIT_AS, (1024 * 1024 * 1024, 1024 * 1024 * 1024))
        # File size limit (bytes) - 100MB
        resource.setrlimit(resource.RLIMIT_FSIZE, (100 * 1024 * 1024, 100 * 1024 * 1024))
    except Exception as e:
        print(f"Warning: Could not set resource limits: {e}", file=sys.stderr)

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
        if sys.argv[1] == '-c':
            # Execute code directly
            if len(sys.argv) > 2:
                exec(sys.argv[2])
            else:
                print("Error: No code provided with -c option", file=sys.stderr)
                sys.exit(1)
        else:
            # Execute script file
            script_path = sys.argv[1]
            sys.argv = sys.argv[1:]
            
            try:
                with open(script_path, 'rb') as f:
                    code = compile(f.read(), script_path, 'exec')
                    exec(code, {'__name__': '__main__'})
            except FileNotFoundError:
                print(f"Error: Script file '{script_path}' not found", file=sys.stderr)
                sys.exit(1)
    else:
        # Interactive mode
        import code
        code.interact(banner="LLMSafeSpace Python Environment", exitmsg="")
