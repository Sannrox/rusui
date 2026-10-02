# Evaluate recorded reviews

This guide replays the development corpus and, when available, an
operator-owned held-out split. It runs dry-run apply against a fake GitHub;
no model or live GitHub write is involved. See the [corpus reference](../eval/set.md)
for the data format and measures, and the [historical replay](../eval/results.md)
for one recorded result.

## Prerequisites

From a source checkout, build the binary and choose its path:

```bash
make all
RUSUI_BIN="_output/local/bin/$(go env GOOS)/$(go env GOARCH)/rusui"
```

If a matching release binary is already on `PATH`, set `RUSUI_BIN=rusui`
instead. A held-out split is optional and stays outside the repository.

## Replay and inspect

```bash
"$RUSUI_BIN" eval                                   # development split, Markdown
"$RUSUI_BIN" eval -heldout ~/rusui-heldout.json     # add the operator-owned split
"$RUSUI_BIN" eval -heldout FILE -gate comment       # exit 4 fail, 5 incomplete
"$RUSUI_BIN" eval -heldout FILE -out eval-reports   # keep a JSON report; never overwrites
"$RUSUI_BIN" eval -heldout FILE -check PRIOR.json   # exit 3 if PRIOR came from other inputs
```

The default command prints a Markdown report. A held-out report includes
its digest and totals but not its cases. Check the reported revision and
input digest before comparing results. Exit 4 means the requested gate
failed; exit 5 means its evidence is incomplete.

## Add a development case

Add development cases for new situations and regenerate `results.md`.
Move a case to held-out only by copying it into the operator's file;
held-out cases are never committed.

Run `go test ./eval -update`, inspect the change to
[results.md](../eval/results.md), and run `go test ./eval` before committing.
Keep operator-owned held-out judgments outside the repository.
