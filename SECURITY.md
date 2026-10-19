# Security policy / Sicherheit

The panel controls Docker and runs as root, so security issues matter a lot.

**Please do not open a public issue for a vulnerability.** Report it privately via GitHub: *Security → Report a vulnerability* on this repository. You will get an answer within a few days; fixes are released as a new version and mentioned in the changelog.

Only the latest release is supported. Please update before reporting.

Hardening tips:
- Do not expose the panel to the internet without HTTPS and two-factor login for every admin.
- Give friends the *viewer* or *operator* role, limited to their servers, instead of admin.
- API keys never get admin rights; delete keys you no longer use.

---

**Sicherheitslücken bitte nicht als öffentliches Issue melden**, sondern vertraulich über GitHub: *Security → Report a vulnerability* in diesem Repository. Unterstützt wird nur die neueste Version.
