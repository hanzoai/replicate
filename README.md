Hanzo Replicate
==========

Hanzo Replicate is a standalone disaster recovery tool for SQLite. It runs as a
background process and safely replicates changes incrementally to another file
or S3. Replicate only communicates with SQLite through the SQLite API so it
will not corrupt your database.

This is a fork of [Litestream](https://github.com/benbjohnson/litestream) by
Ben Johnson, rebranded and extended for the Hanzo infrastructure stack.

## Docker

```
ghcr.io/hanzoai/replicate:latest
```

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
