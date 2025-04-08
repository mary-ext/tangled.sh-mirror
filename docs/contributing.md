# tangled contributing guide

## commit guidelines

We follow a commit style similar to the Go project. Please keep commits:

* **atomic**: each commit should represent one logical change  
* **descriptive**: the commit message should clearly describe what the
change does and why it's needed

### message format

``` 
<service/top-level directory>: <package/path>: <short summary of change>


Optional longer description, if needed. Explain what the change does and
why, especially if not obvious. Reference relevant issues or PRs when
applicable. These can be links for now since we don't auto-link
issues/PRs yet. 
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

- PRs get merged as a single commit, so keep PRs small and focused. Use
the above guidelines for the PR title and description.
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

For larger changes—especially those introducing new features,
significant refactoring, or altering system behavior—please open a
proposal first. This helps us evaluate the scope, design, and potential
impact before implementation.

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
