# gh-offload

**Run any command on a GitHub Actions runner, straight from your terminal, using your working tree exactly as it is right now.**

![gh-offload building itself on a GitHub ARM runner from an x86_64 machine, then file showing the returned binary is ARM aarch64](docs/demo.gif)

<sub>A real run: gh-offload builds itself on an ARM runner and the binary comes back. Pauses longer than 1.5s are shortened. Recorded on a runner by <a href=".github/workflows/demo.yml">demo.yml</a>.</sub>

```sh
gh offload -- cargo build --release
```

No commit, no push, no YAML to write per project. Uncommitted and untracked files go along. You get the output, the command's exit code, and any files you ask for back in your working tree.

```text
$ gh offload --os linux-arm --get target/release/homelab-hub -- cargo build --release --locked -p homelab-hub
› snapshot: HEAD (006ad87) + 1 changed: hub/src/main.rs
› running on ubuntu-24.04-arm: https://github.com/…/actions/runs/43680839108
› waiting for a ubuntu-24.04-arm runner
› Check out snapshot
› Restore build cache
› Run command
› Collect files
› Upload result
   Compiling homelab-hub v0.16.0 (/home/runner/work/…/hub)
    Finished `release` profile [optimized] target(s) in 56.60s
› brought back 1 file(s): target/release/homelab-hub
› exit code 0 after 1m30s

$ file target/release/homelab-hub
target/release/homelab-hub: ELF 64-bit LSB pie executable, ARM aarch64, version 1 (SYSV), dynamically linked, …
```

That's a real run from a private repo, with an uncommitted edit. The first build took 2m36s end to end. The second reused the cache and took 1m30s, and the laptop that started it has 8 GB of RAM and can't finish this build itself.

## Why

I built this because my laptop has 8 GB of RAM, and a release build of my own project for ARM kept getting killed for running out of memory. GitHub already gives every repository powerful Linux, ARM, macOS and Windows machines. Getting your *current* code onto one of them normally means committing, pushing, editing a workflow and clicking through the Actions tab, then downloading a zip.

gh-offload turns that into one command:

- **Weak laptop or Chromebook?** Run the heavy build or test suite on a 4-core runner instead.
- **No Mac, no Windows PC, no ARM box?** Use `--os macos`, `--os windows` or `--os linux-arm` to check that it builds and the tests pass there before you push.
- **Coding agent on a small machine?** `--json` gives it the output, exit code and file list in one object.

## Install

```sh
gh extension install lawless-commits/gh-offload
```

It needs the [GitHub CLI](https://cli.github.com) (logged in with `gh auth login`) and git 2.31 or newer.

## Set up a repository (once)

```sh
gh offload init
```

This opens a pull request that adds [`.github/workflows/offload.yml`](internal/workflow/offload.yml). GitHub only lets you start a workflow from the command line once it is on the default branch, so merge the PR first. `init` never touches your working tree or current branch. To commit the file yourself instead, use `gh offload init --no-pr`, or `--print` to just see it.

## Use

```sh
gh offload -- make test                                  # Linux x64 (ubuntu-latest)
gh offload --os macos -- 'npm ci && npm test'            # one argument = a bash command
gh offload --os windows -- cargo test
gh offload --os linux-arm --get 'dist/**' -- make dist   # bring files back
gh offload --runner ubuntu-24.04 -- ./ci/check.sh        # any runs-on label
gh offload --json -- go test ./... | jq .exit_code       # for scripts and agents
```

| Flag | |
|---|---|
| `--os <name>` | `linux` (default), `linux-arm`, `macos`, `windows`, `windows-arm` |
| `--runner <label>` | any `runs-on` label instead of `--os` |
| `--get <path>` | file, directory or glob to bring back, relative to the repo root; repeatable |
| `--timeout <min>` | job timeout, default 60, at most 360 |
| `--no-cache` | skip restoring and saving the dependency and build cache |
| `--include <path>` | upload a file even though it looks like a secret; repeatable |
| `--keep` | keep the snapshot branch afterwards |
| `--json` | print one JSON object (`exit_code`, `output`, `files`, `run_url`, …) |
| `-y`, `--yes` | don't ask before running in a public repository |
| `-C <dir>`, `--remote <name>` | pick the repository and the remote |

gh offload exits with **the command's own exit code**, so `gh offload -- make test && git push` does what you'd expect. 124 means the command didn't finish before the timeout, 125 means gh offload itself failed, and 130 means you interrupted it. Ctrl-C cancels the run on GitHub as well.

## How it works

1. **Snapshot.** gh offload copies your index to a temporary file, runs `git add --all` against the copy (which respects `.gitignore`), and turns it into a commit on top of `HEAD` with `write-tree` and `commit-tree`. Your real index, branch and files are never touched.
2. **Push.** That commit goes to a temporary branch, `offload/<id>`, with `[skip ci]` in the message so your other workflows don't run on it.
3. **Run.** `offload.yml` is started on your default branch with the snapshot commit as an input. It checks out the commit, restores a cache keyed on your lockfiles (Cargo, npm, pnpm, yarn, Go, pip and uv), and runs your command with bash.
4. **Return.** The output, exit code and `--get` files (as a tarball, so executable bits survive) come back as an artifact kept for one day. gh offload downloads it, prints the output and unpacks the files into your working tree.
5. **Clean up.** The temporary branch is deleted, even if the command fails or you press Ctrl-C.

Runs start from the default branch so that every snapshot shares one cache scope. That makes the second build much faster than the first. Caches made on a throwaway branch could never be used again.

## Safety

- **Secrets.** Before uploading, gh offload refuses changed or untracked files that look like credentials (`.env`, `*.pem`, `*.key`, `id_rsa`, `credentials.json`, `.secrets/` and similar). Add them to `.gitignore`, or pass `--include <path>` if you really want them on the runner. Ignored files are never uploaded.
- **Public repositories.** In a public repo the snapshot branch, the command and its output are public while the run exists. gh offload asks before it starts, unless you pass `--yes`.
- **The workflow.** Its token has `contents: read` only, every action is pinned to a commit SHA, and inputs reach its scripts through environment variables. Nothing is pasted into a script with `${{ }}`. Only people with write access to the repository can start it.
- **Unpacking.** Returned files can't land outside the repository. Absolute paths and `..` are refused, and symlinks are skipped.

## Cost

Standard GitHub-hosted runners are free for public repositories. Private repositories use your account's included Actions minutes, and macOS and Windows minutes count for more than Linux ones. See [GitHub's billing docs](https://docs.github.com/en/billing/managing-billing-for-your-products/managing-billing-for-github-actions/about-billing-for-github-actions) for the current numbers. Some runner labels, such as ARM, may not be available to private repositories on every plan. If a run sits in the queue, gh offload tells you after five minutes.

## Not yet

- **Live output.** GitHub's API only returns a job's log once the job has finished. While the command runs you see each step as it starts, and the full output arrives at the end.
- **Interactive shells.** For a shell on a runner, [action-tmate](https://github.com/mxschmitt/action-tmate) does that.
- **Submodule changes.** Submodules are checked out at the commit your repository points to, not with your local edits.

## Compared with

- **[act](https://github.com/nektos/act)** runs your workflows on *your* machine in Docker. gh-offload runs a command on *GitHub's* machines.
- **Pushing a branch and watching CI** works, but means a commit per try, a workflow that runs your whole pipeline, and a zip to download for any file you want.
- **Hosted remote builders** (Depot and others) are paid services with their own runners. gh-offload uses the runners and minutes your GitHub account already has.

## FAQ

**Is this allowed by GitHub's terms?**
GitHub's [terms for Actions](https://docs.github.com/en/site-policy/github-terms/github-terms-for-additional-products-and-features#actions) allow using runners to build and test the software project in the repository, and rule out activity unrelated to it, such as crypto mining or running a serverless app. gh-offload is meant for the first kind: building and testing the repository you run it from. Using it for anything else is up to you, and so is the risk.

**How much time does each run add?**
About 20 seconds for a trivial command, most of it waiting for a runner to start and for the result artifact to come back. It's built for commands that take minutes, not for running `ls`.

**What's installed on the runner?**
Whatever GitHub's [runner images](https://github.com/actions/runner-images) include: Git, Docker on Linux, and common toolchains such as Go, Node, Python, Java and Rust. Install anything else as part of your command, for example `gh offload -- 'sudo apt-get install -y libfoo-dev && make'`.

**Why does it need a workflow in my repository?**
GitHub only lets you start a workflow that is already on the default branch, so `gh offload init` adds one through a pull request. It's a single file you can read before you merge it. After that, every project uses the same workflow, whatever the command.

**Can coding agents use it?**
Yes. `--json` prints one JSON object with the output, the exit code and the files that came back, and the exit code always matches the command's. An agent on a small machine can run the heavy tests remotely.

**Does it work with GitHub Enterprise Server or self-hosted runners?**
`--runner` takes any `runs-on` label, including self-hosted and larger runners, but neither those nor GitHub Enterprise Server have been tested yet. Reports are welcome.

## Development

```sh
go test ./...
go build -o gh-offload . && gh extension install .
```

CI runs the unit tests on Linux, macOS and Windows. It then runs [`scripts/e2e.sh`](scripts/e2e.sh), which offloads a real command from this repository to Linux, Windows and macOS runners and checks the output, the exit code, the returned files and that the branch was cleaned up.

gh-offload was built with [Claude Code](https://claude.com/claude-code), which wrote most of the code. Every change was tested in the CI described above, on GitHub's runners.

## License

[MIT](LICENSE)
