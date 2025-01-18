# docs-from-repo

A CLI tool to compile Markdown files from one or more Git repositories into easily readable documents.

## Prerequisites

- Git installed on your PATH

## Usage

Single repository:

    ./docs-from-repo https://github.com/example/repo.git

Multiple repositories:

1. Create a file (e.g. `repos.txt`):

       https://github.com/example/repo.git
       https://github.com/example/repo.git

2. Run:

       ./docs-from-repo repos.txt

## Customizing Output

By default, documentation goes into `output`. To specify a different directory:

    ./docs-from-repo -output-dir /path/to/mydocs https://github.com/example/repo.git

## Development

    bazel run //src:cli -- -output-dir /path/to/output https://github.com/example/repo.git

## Future Improvements

- Add test coverage
