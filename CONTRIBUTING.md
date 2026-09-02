# Contributing

## This project is not accepting external pull requests yet

That is a deliberate posture, not an oversight, and it is worth stating plainly
rather than leaving it to be inferred from silence. Two independent reasons:

1. **Triage capacity.** This is presently a solo-maintained project; a review
   queue for external PRs is a standing cost that does not yet have anyone
   assigned to it.
2. **Relicensing optionality.** This is the more important one, and it is worth
   being explicit about rather than discovering it by accident later.
   [`docs/adr/ADR-0075-open-source-licence.md`](docs/adr/ADR-0075-open-source-licence.md)
   set the project's license to Apache-2.0; commercialization of this product is
   an **open possibility**, not a concrete plan, as of that decision. **Merging a
   substantial external contribution, not adopting the DCO below, is what spends
   the option to relicense later** — once one is merged, relicensing requires
   that contributor's agreement. Certifying provenance (the DCO, below) is a
   separate concern from preserving that option; do not conflate the two. See
   [`docs/adr/ADR-0076-contribution-provenance-dco.md`](docs/adr/ADR-0076-contribution-provenance-dco.md)
   for the full reasoning, including its own ⚠ warning that opening PRs while
   commercialization is still merely possible would spend that option "without
   anyone deciding to."

Bug reports and questions, filed as issues, are welcome in the meantime. Please
don't invest time preparing a pull request until this notice is updated — it
won't be merged, however good it is.

---

## When contributions do open: provenance (DCO 1.1)

This is recorded now so the process is decided before it is needed, not
improvised under pressure the first time an external contribution shows up.

This project uses the **Developer Certificate of Origin (DCO) 1.1** — not a
Contributor License Agreement (CLA). The full text is reproduced below. In short:
by signing off a commit, you certify that you wrote it (or otherwise have the
right to submit it under the project's license).

Sign off every commit with:

```sh
git commit -s
```

This appends a `Signed-off-by: Your Name <your.email@example.com>` trailer to the
commit message, using the name and email from your git config. Use your real name
and a real email address — anonymous or pseudonymous sign-offs do not satisfy the
DCO's purpose.

### ⚠ Enforcement is a review step, not an automated check

This project has no continuous integration today. There is no bot that will
reject an unsigned commit, and none should be assumed. Sign-off is checked
**by a human, during review**, not by a gate. If and when this project stands up
CI, a DCO check can be wired into it at that time — until then, treat this as a
manual convention, not an enforced one.

### Why DCO and not a CLA

A CLA is the instrument that would actually preserve relicensing or
dual-licensing rights going forward. It was considered and rejected *for now*
(not permanently) because, with effectively zero contributors today, it would be
the single largest friction point for a first-time contributor and would price in
an intention — a commercial plan — that does not yet exist. **The DCO does not do
what a CLA would do here**: it certifies that a contribution's provenance is
clean, nothing more. The project's "not accepting PRs yet" posture above is doing
the actual work of keeping the relicensing option open; don't mistake the DCO for
that.

If commercialization moves from *possible* to *intended*, or before merging the
first substantial external contribution — whichever comes first — the CLA
question gets reopened before that merge happens, not after.

---

## Developer Certificate of Origin 1.1

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.
1 Letterman Drive
Suite D4700
San Francisco, CA, 94129

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```
