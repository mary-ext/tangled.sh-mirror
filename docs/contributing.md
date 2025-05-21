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

The affected package/directory can be truncated down to just the relevant dir
should it be far too long. For example `pages/templates/repo/fragments` can
simply be `repo/fragments`.

### general notes

- PRs get merged "as-is" (fast-forward) -- like applying a patch-series
using `git am`. At present, there is no squashing -- so please author
your commits as they would appear on `master`, following the above
guidelines.
- Use the imperative mood in the summary line (e.g., "fix bug" not
"fixed bug" or "fixes bug").
- Try to keep the summary line under 72 characters, but we aren't too
fussed about this.
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
