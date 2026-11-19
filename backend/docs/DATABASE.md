# PostgreSQL foundation

Phase 01 uses Tern v2.4.3 as a separate migration command. Tern records the version in `public.hive_schema_version` and applies each migration in a PostgreSQL transaction under an advisory lock. Migration 001 is owned by Backend Engineering and creates the shared application namespace `hive`. Each future business table belongs to its owning domain under [Architecture §9.2](ARCHITECTURE.md#92-postgresql--modular-database-architecture--evolutionary-database-design). Backend access roles, tenant SQL and application connection pooling will be reviewed with the first domain slice. [Tern documentation](https://github.com/jackc/tern/blob/v2.4.3/README.markdown)

## Local verification

From `backend/`, install the same CLI version as CI and run the checks with Go 1.26.5 and a local Docker engine.

```sh
go install github.com/jackc/tern/v2@v2.4.3
bash scripts/check-architecture.sh
PATH="$(go env GOPATH)/bin:$PATH" bash scripts/test-migrations.sh
```

The integration script creates a PostgreSQL 18 container pinned by image digest, publishes its port on loopback, and removes the container at exit. It runs fresh apply and retry using the repository SQL. It then tests an additive upgrade from 001 with a disposable fixture, two concurrent migrators and a migration that must roll back on SQL failure. The fixture migrations live in temporary storage.

## Migration workflow

- Add a sequential `NNN_description.sql` file with an owner comment. Keep applied files immutable and use a new version for corrections.
- Prefer additive changes. Review older application compatibility, lock duration, tenant constraints and data recovery. For breaking changes, use Expand → Migrate → Contract with a verified backfill and separate review before removing the old representation.
- Use a direct PostgreSQL connection for the migrator and a separately scoped DDL role. Configure the explicitly authorized target and credentials outside the repository with settings appropriate to the environment, including TLS and bounded lock and statement timeouts.
- With the target verified, `tern status --config migrations/tern.conf --migrations migrations` shows schema version. The forward command is `tern migrate --config migrations/tern.conf --migrations migrations`. Phase 01 has a forward-only baseline. Recover a failed transactional migration by correcting the unapplied change and retrying. Correct a committed migration through a reviewed forward change.
