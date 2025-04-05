# Review guidelines

When you have finished a project:

* Run `gofmt -w .` or `goimports -w .` to format the code
* Run the tests with `go test ./...`
* Fix all failing tests
* Fix all warnings (you can use `golint` to generate warnings)
* Run `git diff --name-only main` to get a list of the files you've changed
* Review each of them according to the guidelines in the project memory
* Review the todo list and verify all the tasks are completed
* Review the project memory, and evaluate what should be added to the application memory
* Review the development guidelines, and evaluate what should be added to the development guidelines
