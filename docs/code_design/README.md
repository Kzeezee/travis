# Code Design Docs

Status: Phase 0 documentation index.

`docs/DESIGN.md` is still normative. This folder is the smaller code-facing guide:
where code should live, which patterns Travis uses, and why those patterns exist.

Ignore everything under `archive/`; those files are old source material, not active guidance.

## Active Docs

| Group | File | Role |
| --- | --- | --- |
| Map | [codebase_tour.md](codebase_tour.md) | Start here. Shows the folder shape, dependency direction, core patterns, and testing architecture. |
| Rationale | [design_rationales.md](design_rationales.md) | Explains why the main patterns exist and what the code looks like without them. |
| Implementation Example | [ports_and_adapters.md](implementation_example/ports_and_adapters.md) | Shows the main way Travis keeps business logic separate from filesystem, provider, MCP, broker, and other effects. |
| Implementation Example | [error_handling.md](implementation_example/error_handling.md) | Defines how errors cross those seams: expected outcomes, Go errors, adapter translation, event failures, and unexpected bugs. |
| Presentation layer | [presentation_layer.md](presentation_layer.md) | Expands the UI layer rules from the tour: screens/pages orchestrate, components own UI behavior, presenters/hooks translate data. |

## Relationship

```text
codebase_tour.md
  main map and first read
  |
  +-> design_rationales.md
  |     why the main patterns exist
  |
  +-> implementation_example/
  |     ports_and_adapters.md
  |       how business logic calls effects through ports
  |     error_handling.md
  |       how failures are returned or translated across those ports
  |
  +-> presentation_layer.md
        deeper frontend / Bubble Tea structure
```

Events and operations do not have a separate active example file. Keep that rule in
[codebase_tour.md](codebase_tour.md#8-pattern-events-and-operations),
[design_rationales.md](design_rationales.md#3-why-write-event-based-code-this-way),
and [error_handling.md](implementation_example/error_handling.md#event-observer-failure).
