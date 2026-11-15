// Package workflow holds the GitHub Actions workflow that `gh offload init`
// adds to a repository.
package workflow

import _ "embed"

// File is where the workflow lives in a repository.
const File = ".github/workflows/offload.yml"

// Template is the workflow file's content.
//
//go:embed offload.yml
var Template []byte
