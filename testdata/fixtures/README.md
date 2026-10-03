# Fixtures

Kept outside the fixture roots on purpose: a workspace tool would otherwise find this file.

## notes/

A small personal-notes workspace for end-to-end tool tasks (TRA-17), kept liftable into
the 6A scenario harness. Read-only: tests point the workspace root at `notes/` and never
write to it.

Task: "Which note mentions the dentist appointment, and what date is it on?"
Answer: `journal/2026-09-14-café.md`; Tuesday 13 October 2026.

Deliberate properties:
- the answering note is found by content; its name says nothing about dentists
- that note points at `admin/` without naming the file, so a second search is needed
- `admin/clinic-confirmation.md` says "Dental", not "dentist", and has CRLF line endings
- `journal/2026-09-14-café.md` has a non-ASCII (NFC) name
- `admin/insurance.md` and `journal/2026-09-02.md` are decoys

`testdata/** -text` in `.gitattributes` keeps git from rewriting the CRLF file.
