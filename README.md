# docs-from-repo

A CLI tool to compile Markdown files from one or more Git repositories into a master Markdown file.

This is particularly useful if you have documentation spread across multiple Git repositories and want to upload it for use in [NotebookLM](https://notebooklm.google.com/).

For example, if you're learning about Bazel and have several repositories like [rules_oci](https://github.com/bazel-contrib/rules_oci), [bazelisk](https://github.com/bazelbuild/bazelisk/), and [rules_go](https://github.com/bazel-contrib/rules_go/), you can use `docs-from-repo` to collate all the documentation from these repositories. Once collated, you can upload the master Markdown file to NotebookLM and ask questions directly.

## Usage

Download the appropriate binary from [releases](https://github.com/varunchopra/docs-from-repo/releases) (update the binary name and version as needed):

    wget https://github.com/varunchopra/docs-from-repo/releases/download/v0.0.2/docs-from-repo_linux_amd64 -O /usr/local/bin/docs-from-repo

Grant executable permissions:

    chmod +x /usr/local/bin/docs-from-repo

### Single Repository

Run the following command:

    docs-from-repo https://github.com/example/repo.git

### Multiple Repositories

Create a file (e.g., `repos.txt`) containing the list of repositories:

    https://github.com/example/repo.git
    https://github.com/example/repo.git

Run the tool with the file as input:

    docs-from-repo repos.txt

## Customizing Output

By default, documentation goes into `output`. To specify a different directory:

    docs-from-repo -output-dir /path/to/mydocs https://github.com/example/repo.git

## Development

    bazel run //src:cli -- -output-dir /path/to/output https://github.com/example/repo.git

## Future Improvements

- Add test coverage
