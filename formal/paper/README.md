# Paper: the Dex expressiveness theorem

This directory holds the LaTeX source of the paper "Machine-Checked
Expressiveness and Failure Transparency for the Dex Durable Workflow
Programming Model". The PDF keeps its original file name. The paper
describes the semantics in
[`formal/semantics/dex-core-semantics.md`](../semantics/dex-core-semantics.md)
and the Lean proof in [`formal/lean/`](../lean/README.md).

## Build

The built PDF is committed at `docs/static/papers/dex-expressiveness.pdf`.
That is its only home. From the repository root:

```bash
tectonic -o docs/static/papers formal/paper/dex-expressiveness.tex
```

or `make -C formal/paper`. Install [tectonic](https://tectonic-typesetting.github.io)
first (`brew install tectonic`). The first build downloads the TeX packages
it needs. tectonic keeps no intermediate files, so nothing is written next to
the sources.

`make -C formal/paper check` builds the PDF and fails when the log reports an
overfull box, an undefined reference, or an undefined citation.

After any change to a file in this directory, rebuild the PDF and commit it
together with the source change.

## Layout

| File | Content |
| --- | --- |
| `dex-expressiveness.tex` | Preamble, title, abstract, and the list of sections. |
| `sections/*.tex` | One file per section, in order. |
| `references.bib` | The bibliography. Every entry was checked against a publisher page, DBLP, or the authors' copy. Do not add an entry without doing the same. |
| `Makefile` | The build command. |

## Rules for edits

- Every technical claim must match the specification and the Lean code. When
  they differ, follow the code.
- Give Lean names exactly, with `\lean{...}`, and file paths with
  `\file{...}`.
- Numbers in the paper are for the commits of `formal/lean/` named at the
  end of the introduction. Recount them when a commit changes: `wc -l` for
  the sizes, `lake exe inductiveness-fuzz` and `lake exe failure-fuzz` for
  the fuzzes.
- Do not name products built on Dex or commercial workflow products.
