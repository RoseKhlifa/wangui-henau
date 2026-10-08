# Project Working Agreement

## Required delivery workflow

For every repository change, complete the full delivery sequence unless the user explicitly says otherwise:

1. Run checks appropriate to the files changed.
2. Commit all intended project changes with a bilingual Chinese/English commit message, for example: `docs: 记录部署流程 / document deployment workflow`.
3. Push the commit to the configured upstream repository (`origin`).
4. Deploy only after the push succeeds.

Do not report a change as complete before the commit, push, and deployment have all succeeded. Never include secrets, `.env`, database files, or runtime data in commits.

## Server deployment

- Connect from the local machine with `ssh chaoxing`.
- Deploy the server application to `/root/wangui-henau`.
- Prefer incremental `rsync` from the repository root. Preserve server secrets and runtime state by excluding at least `.git/`, `.env`, `data/`, dependency directories, generated build output, and database files.
- After syncing, run `docker compose up -d --build` in `/root/wangui-henau`.
- Verify the `wangui` container is running, inspect recent logs, test `http://127.0.0.1:5555/` on the server, and test the public IP plus port when reachable.
- The public service port is `5555`; keep the server firewall rule for `5555/tcp` available unless the user changes the exposure method.
- Never overwrite or print `WANGUI_MASTER_KEY`. Preserve `/root/wangui-henau/.env` and `/root/wangui-henau/data/` across every deployment.
