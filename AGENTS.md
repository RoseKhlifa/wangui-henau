# Project Working Agreement

## Required delivery workflow

For every repository change, complete the full delivery sequence unless the user explicitly says otherwise:

1. Run checks appropriate to the files changed.
2. Commit all intended project changes with a bilingual Chinese/English commit message, for example: `docs: 记录部署流程 / document deployment workflow`.
3. Push the commit to the configured upstream repository (`origin`).
4. Deploy only after the push succeeds.

Do not report a change as complete before the commit, push, and deployment have all succeeded.

## Public repository privacy

This is a public repository. Before every commit, inspect the staged diff and remove or replace all private values, including:

- real names, student numbers, classes, departments, email addresses, and avatars;
- JWTs, OAuth codes, passwords, encryption keys, cookies, and raw authentication responses;
- private server IPs, SSH aliases, credentials, filesystem details, and infrastructure identifiers;
- users' exact check-in coordinates, dorm locations, database contents, logs, backups, and runtime data.

Use clearly fictional placeholders in examples. Never commit `.env`, database files, captured traffic, production logs, or local deployment configuration. Do not print secrets during diagnostics or deployment.

## Deployment

Read `.codex/deployment.local.md` when it exists and follow its private machine-specific deployment procedure. That file is intentionally ignored by Git. Preserve remote secrets and runtime data across every deployment.
