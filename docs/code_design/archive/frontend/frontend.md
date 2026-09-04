---
name: react-frontend
description: Build or review React frontends using TypeScript, Vite, Tailwind, shadcn/ui, and TanStack Query, Table, or Router. Use for pages, forms, tables, hooks, API integration, query invalidation, and stack-specific component architecture.
---

# React Frontend

Build or review a feature by following the codebase's established frontend path. Project conventions outrank this skill.

## Orient first

1. Read project instructions.
2. Find the closest working feature and follow its structure.
3. Locate the API client, query-key factory, shared components, router, and error utilities.
4. Confirm the server contract before defining frontend types.

Do not introduce a second state, fetching, form, table, or styling convention because it is locally convenient.

## Build

Add only the layers the feature needs, in dependency order:

1. **Types** — mirror the API contract. Separate create, update, list, and detail shapes when their semantics differ. Read [types](references/types.md) for examples.
2. **Server state** — add the query or mutation through the established client and key factory. Invalidate every affected query after mutation. Read [hooks](references/hooks.md).
3. **Composition** — keep pages responsible for wiring data and UI state, not business rules. Read [pages](references/pages.md).
4. **Table** — use the shared table and typed columns only for list behavior. Read [data tables](references/data-table.md).
5. **Form** — use the established schema and form utilities; surface pending, success, and failure states. Read [forms](references/forms.md).
6. **Route and navigation** — extend the canonical router and guards; match sibling paths and labels.

Do not add a layer merely because it appears in this list.

## Review

Start with failures that change behavior or leave the interface stale:

- server data duplicated into local state;
- mutations missing invalidation or error handling;
- raw or unstable query keys;
- loading, empty, error, or responsive states missing;
- API shapes renamed or asserted instead of represented;
- business policy embedded in hooks or page components;
- a bespoke component duplicating an established shared primitive;
- internal IDs, raw JSON, or debug state given user-facing prominence.

Then check component boundaries, TypeScript precision, accessibility, and consistency. Use the [review checklist](references/review-checklist.md) when a full audit is requested.

Rank findings by user impact and architectural reach. Give the concrete fix and preserve good existing patterns; do not produce style commentary without an effect.

## Verify

Run the project's type checker, tests, and formatter. Exercise the feature in the browser when available:

- load, empty, error, and success states;
- mutations and subsequent cache refresh;
- keyboard and narrow-screen behavior;
- no duplicate requests, permanent polling, or visible stale state.

Finish when the feature uses one canonical data path, reflects the actual API contract, reuses the design system, and behaves correctly across its states.
