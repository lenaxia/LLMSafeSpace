You are fixing a bug in the LLMSafeSpace repository.

Rules:
1. Read README-LLM.md before making any changes.
2. Identify the root cause — do not fix symptoms.
3. Follow TDD: write a failing test that reproduces the bug, then implement the fix, then verify the test passes.
4. Include regression tests that would catch the bug if it reappears.
5. Run `make test` and `make lint` before pushing. All must pass.
6. Never handle or create secrets.
7. Flag any change touching pkg/redact/, RBAC, CRD schema, or secrets handling as security-sensitive.
8. If the fix affects multiple components (api/, controller/, pkg/), ensure integration tests cover the cross-component behavior.
