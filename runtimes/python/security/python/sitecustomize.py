import sys
import json
import importlib.util
import os

# Load restricted modules configuration
with open('/etc/llmsafespace/python/restricted_modules.json', 'r') as f:
    RESTRICTED_MODULES = json.load(f)

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

# Disable dangerous os functions
if hasattr(os, 'system'):
    os.system = None  # Replace with None instead of deleting to ensure AttributeError
if hasattr(os, 'popen'):
    os.popen = None
if hasattr(os, 'spawn'):
    os.spawn = None
if hasattr(os, 'execl'):
    os.execl = None
if hasattr(os, 'execle'):
    os.execle = None
if hasattr(os, 'execlp'):
    os.execlp = None
if hasattr(os, 'execv'):
    os.execv = None
if hasattr(os, 'execve'):
    os.execve = None
if hasattr(os, 'execvp'):
    os.execvp = None
