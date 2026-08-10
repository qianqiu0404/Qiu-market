# Sensitive local files and repository gate

Qiu Market treats dotenv files, credentials, private keys, wallet/TSS material and database or infrastructure state as local runtime inputs, never source artifacts. The repository root `.env` is intentionally untracked; `.env.example` is the reviewed key-name template and must never be generated from a real dotenv file.

## Local setup

Copy or create the local file without committing it:

```bash
cp .env.example .env
chmod 600 .env
```

Populate it through the approved local secret channel. Do not paste values into issues, logs, fixtures, tests, screenshots or example/template files. If a real secret was ever committed, removing the path is not sufficient: rotate the credential and follow the repository host's history-remediation procedure.

## Gate

```bash
make security-paths-test
make security-paths
make security-env-templates-test
make security-env-templates
```

The gate reads only the Git index path list (`git ls-files`), never file contents. It rejects tracked dotenv paths, private-key/keystore extensions, seed or mnemonic paths, TSS shares, wallet state, database/Terraform state, credential-store paths and files under secret/database-cluster directories. A reviewed filename containing an explicit `.example` or `.template` token is allowed; this is a path exemption, not permission to place real values in it.

Stable rule IDs are used in failures and review evidence:

| Rule | Rejected tracked path class |
|---|---|
| `S001` | control characters that could obscure a path in logs |
| `S010` | dotenv / envrc |
| `S020` | private key or keystore |
| `S030` | seed phrase or mnemonic |
| `S040` | wallet state |
| `S050` | database, PostgreSQL cluster, dump or Terraform state |
| `S060` | credential file or credential-store path |
| `S070` | file under a secrets directory |
| `S080` | TSS share material |

The test creates empty fake paths inside an automatically removed temporary Git repository. It checks both rejection and `.gitignore` behavior and never reads or prints a secret value. CI runs syntax, fixture tests and the current-index scan in `repository-contracts`.

The env-template gate reads only tracked dotenv files whose basename explicitly ends in `.example` or `.template`; it never opens a local `.env` or compares a template with one. Output is limited to the reviewed path, declared-key count, and error category. Blank lines and comments are allowed; every other line must be a unique `KEY=value` assignment, optionally prefixed by exactly one `export `, with a portable shell-style key. Templates must be regular, non-symlink files, no larger than 64 KiB, and contain at least one key.

| Rule | Rejected env-template condition |
|---|---|
| `E000` | invalid checker invocation |
| `E001` | non-regular file or symlink |
| `E010` | template exceeds the size bound |
| `E020` | invalid assignment syntax or control character |
| `E030` | invalid key syntax |
| `E040` | duplicate key |
| `E050` | no declared keys |
| `E060` | repository has no tracked dotenv template |

## Incident response

1. Stop adding or copying the affected file; do not print or diff it while investigating.
2. Record path metadata only, remove it from the index with `git rm --cached -- <path>`, and add an ignore rule while preserving the local disk file.
3. Assume an exposed credential is compromised. Revoke/rotate it at its authority and invalidate derived sessions, wallet shares or database access as applicable.
4. Ask the repository owner to decide whether history rewrite is required. Coordinate that operation separately because it changes clones and open branches.
5. Run both security path targets and the normal repository gates before review.
