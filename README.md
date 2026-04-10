Hanzo Replicate
==========

Hanzo Replicate is a standalone disaster recovery tool for SQLite. It runs as a
background process and safely replicates changes incrementally to another file
or S3. Replicate only communicates with SQLite through the SQLite API so it
will not corrupt your database.

Module: `github.com/hanzoai/replicate`

This is a fork of [Litestream](https://github.com/benbjohnson/litestream) by
Ben Johnson, rebranded and extended for the Hanzo infrastructure stack.

## Docker

```
ghcr.io/hanzoai/replicate:latest
```

## Features

- WAL streaming to S3 (continuous incremental replication)
- End-to-end encryption via `luxfi/age` v1.4.0 (X25519, PQ upgrade path via X-Wing/ML-KEM-768)
- Compatible with `hanzoai/s3` (MinIO)
- Init container restore + sidecar replication pattern for K8s

## Encryption

Replicated data is encrypted before leaving the process using `luxfi/age`.
Configure encryption in `litestream.yml`:

```yaml
dbs:
  - path: /data/db.sqlite
    replicas:
      - type: s3
        bucket: my-bucket
        path: service/pod-0
        endpoint: s3.local:9000
        age-identities: /secrets/age-identity.txt
        age-recipients: /secrets/age-recipients.txt
```

Files on S3 are stored as `.zap.age` (age-encrypted ZAP binary format).

## Usage

```bash
replicate replicate [arguments]
replicate restore [arguments] DB_PATH
replicate version
```

## Upstream

For the original documentation and installation instructions, visit the
[Litestream web site](https://litestream.io).

## License

See [LICENSE](LICENSE).

## Acknowledgements

All credit to [Ben Johnson](https://github.com/benbjohnson) and the Litestream
contributors for the original project.
