#!/usr/bin/env node

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const Module = require('module');

// Load restricted modules configuration
const RESTRICTED_MODULES = JSON.parse(
  fs.readFileSync('/etc/llmsafespace/nodejs/restricted_modules.json', 'utf8')
);

// Original require function
const originalRequire = Module.prototype.require;

// Override require to restrict dangerous modules
Module.prototype.require = function(moduleName) {
  if (RESTRICTED_MODULES.blocked.includes(moduleName)) {
    throw new Error(`Requiring '${moduleName}' is not allowed for security reasons`);
  }
  
  if (RESTRICTED_MODULES.warning.includes(moduleName)) {
    console.warn(`WARNING: Requiring '${moduleName}' may pose security risks`);
  }
  
  return originalRequire.apply(this, arguments);
};

// Set up secure execution context
const setupSecureContext = () => {
  // Disable process.exit
  process.exit = (code) => {
    console.error('process.exit() is disabled for security reasons');
    if (code !== 0) {
      throw new Error(`Attempted to exit with code ${code}`);
    }
  };
  
  // Restrict child_process
  try {
    delete require.cache[require.resolve('child_process')];
    require.cache[require.resolve('child_process')] = {
      exports: {
        exec: () => { throw new Error('child_process.exec is disabled'); },
        spawn: () => { throw new Error('child_process.spawn is disabled'); },
        execSync: () => { throw new Error('child_process.execSync is disabled'); },
        spawnSync: () => { throw new Error('child_process.spawnSync is disabled'); }
      }
    };
  } catch (err) {
    console.warn('Warning: Could not restrict child_process module');
  }
  
  // Set resource limits
  try {
    if (typeof process.setResourceLimits === 'function') {
      process.setResourceLimits({
        maxOldGenerationSizeMb: 512,
        maxYoungGenerationSizeMb: 128,
        codeRangeSizeMb: 64
      });
    }
  } catch (err) {
    console.warn('Warning: Could not set resource limits');
  }
};

// Set up secure context
setupSecureContext();

// Execute the provided script
if (process.argv.length > 2) {
  const arg = process.argv[2];
  
  if (arg === '-e' && process.argv.length > 3) {
    // Execute code directly
    try {
      eval(process.argv[3]);
    } catch (error) {
      console.error(`Error executing code: ${error.message}`);
      process.exit(1);
    }
  } else {
    // Execute script file
    const scriptPath = arg;
    process.argv = process.argv.slice(1); // Adjust argv to match normal node behavior
    
    try {
      // Check if TypeScript file
      if (scriptPath.endsWith('.ts')) {
        require('ts-node').register({
          project: process.env.TS_NODE_PROJECT
        });
      }
      require(path.resolve(scriptPath));
    } catch (error) {
      console.error(`Error executing script: ${error.message}`);
      process.exit(1);
    }
  }
} else {
  // Interactive mode (REPL)
  const repl = require('repl');
  repl.start({
    prompt: 'LLMSafeSpace Node.js > ',
    useGlobal: true
  });
}
