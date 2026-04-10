# replicate-vfs

Replicate VFS extension for SQLite — distributed as a Ruby gem.

This gem bundles the [Replicate](https://replicate.io) VFS shared library
so you can load it directly into a SQLite connection.

## Installation

```bash
gem install replicate-vfs
```

Or add to your Gemfile:

```ruby
gem "replicate-vfs"
```

## Usage

```ruby
require "replicate_vfs"

db = SQLite3::Database.new(":memory:")
ReplicateVfs.load(db)
```

To get the path to the shared library:

```ruby
path = ReplicateVfs.loadable_path
```

## Platform Support

| Platform | Architecture |
|----------|-------------|
| Linux | x86_64, aarch64 |
| macOS | x86_64, arm64 |

## License

Apache-2.0 — see [LICENSE](https://github.com/hanzoai/replicate/blob/main/LICENSE).
