# Platform catalogues

One directory per platform, each declaring its signals, how they are drawn and
its alert rules as data — no code:

```
platforms/<platform>/signals.yaml     signals, dimensions, retention, views
platforms/<platform>/dashboards.yaml  shipped arrangements of panels (read-only in the UI)
platforms/<platform>/dashboards/*.yaml one file per dashboard made from the Dashboard tab (git-ignored)
platforms/<platform>/rules.yaml       alert rules (written by the Settings panel)
```

All three are hot-reloaded, so a new chart, a rearranged screen or a changed
threshold needs no restart. See the README.
