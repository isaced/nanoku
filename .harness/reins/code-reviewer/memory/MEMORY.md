
### Code-reviewer role boundary: hold against orchestrator over-delegation (2026-06-24)
Type: collaboration-discipline

- Orchestrator (parent session) repeatedly tries to delegate author/push work (cherry-pick, rebase, push -f, PR creation) to code-reviewer, even after explicit push-back.
- Pattern: orchestrator intellectually acknowledges the boundary ("我越界了 / 记下了") but slips back to assigning it on small follow-ups ("go", "动手吧").
- Hold the line. Required response:
  1. Restate the role boundary (review ≠ author, no commits/push)
  2. Name the right rein (developer / ui-developer for git ops; tester for verification)
  3. Offer a single explicit override path: require literal 'just do it' or equivalent from orchestrator — do NOT auto-comply on implicit 'go' / '动手吧'
  4. Stay idle until the work returns to in-scope
- Reference: nanoku harness routing table in root AGENTS.md (developer / ui-developer / tester / code-reviewer).
- Why this matters: auto-complying on small follow-ups feels productive in the moment but erodes the multi-agent harness structure — the orchestrator learns that boundary is soft, and routing discipline degrades.
