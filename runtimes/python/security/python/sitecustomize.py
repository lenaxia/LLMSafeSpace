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

# Disable dangerous os functions by replacing them with functions that raise AttributeError
def _raise_attribute_error(*args, **kwargs):
    raise AttributeError("This function is disabled for security reasons")

if hasattr(os, 'system'):
    os.system = _raise_attribute_error
if hasattr(os, 'popen'):
    os.popen = _raise_attribute_error
if hasattr(os, 'spawn'):
    os.spawn = _raise_attribute_error
if hasattr(os, 'execl'):
    os.execl = _raise_attribute_error
if hasattr(os, 'execle'):
    os.execle = _raise_attribute_error
if hasattr(os, 'execlp'):
    os.execlp = _raise_attribute_error
if hasattr(os, 'execv'):
    os.execv = _raise_attribute_error
if hasattr(os, 'execve'):
    os.execve = _raise_attribute_error
if hasattr(os, 'execvp'):
    os.execvp = _raise_attribute_error
