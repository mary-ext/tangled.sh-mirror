# tangled contributing guide

## commit guidelines

We follow a commit style similar to the Go project. Please keep commits:

* **atomic**: each commit should represent one logical change
* **descriptive**: the commit message should clearly describe what the
change does and why it's needed

### message format

```
<service/top-level directory>: <affected package/directory>: <short summary of change>


Optional longer description can go here, if necessary. Explain what the
change does and why, especially if not obvious. Reference relevant
issues or PRs when applicable. These can be links for now since we don't
auto-link issues/PRs yet.
```

Here are some examples:

```
appview: state: fix token expiry check in middleware

The previous check did not account for clock drift, leading to premature
token invalidation.
```

```
knotserver: git/service: improve error checking in upload-pack
```


### general notes

- PRs get merged "as-is" (fast-forward) -- like applying a patch-series
using `git am`. At present, there is no squashing -- so please author
your commits as they would appear on `master`, following the above
guidelines.
- If there is a lot of nesting, for example "appview:
pages/templates/repo/fragments: ...", these can be truncated down to
just "appview: repo/fragments: ...". If the change affects a lot of
subdirectories, you may abbreviate to just the top-level names, e.g.
"appview: ..." or "knotserver: ...".
- Keep commits lowercased with no trailing period.
- Use the imperative mood in the summary line (e.g., "fix bug" not
"fixed bug" or "fixes bug").
- Try to keep the summary line under 72 characters, but we aren't too
fussed about this.
- Follow the same formatting for PR titles if filled manually.
- Don't include unrelated changes in the same commit.
- Avoid noisy commit messages like "wip" or "final fix"—rewrite history
before submitting if necessary.

## proposals for bigger changes

Small fixes like typos, minor bugs, or trivial refactors can be
submitted directly as PRs.

For larger changes—especially those introducing new features, significant
refactoring, or altering system behavior—please open a proposal first. This
helps us evaluate the scope, design, and potential impact before implementation.

### proposal format

Create a new issue titled:

```
proposal: <affected scope>: <summary of change>
```

In the description, explain:

- What the change is
- Why it's needed
- How you plan to implement it (roughly)
- Any open questions or tradeoffs

We'll use the issue thread to discuss and refine the idea before moving
forward.

## developer certificate of origin (DCO)

We require all contributors to certify that they have the right to
submit the code they're contributing. To do this, we follow the
[Developer Certificate of Origin
(DCO)](https://developercertificate.org/).

By signing your commits, you're stating that the contribution is your
own work, or that you have the right to submit it under the project's
license. This helps us keep things clean and legally sound.

To sign your commit, just add the `-s` flag when committing:

```sh
git commit -s -m "your commit message"
```

This appends a line like:

```
Signed-off-by: Your Name <your.email@example.com>
```

We won't merge commits if they aren't signed off. If you forget, you can
amend the last commit like this:

```sh
git commit --amend -s
```

If you're submitting a PR with multiple commits, make sure each one is
signed.

For [jj](https://jj-vcs.github.io/jj/latest/) users, you can run the following command
to make it sign off commits in the tangled repo:

```shell
# Safety check, should say "No matching config key..."
jj config list templates.commit_trailers
# The command below may need to be adjusted if the command above returned something.
jj config set --repo templates.commit_trailers "format_signed_off_by_trailer(self)"
```

Refer to the [jj
documentation](https://jj-vcs.github.io/jj/latest/config/#commit-trailers)
for more information.
