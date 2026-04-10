Gem::Specification.new do |s|
  s.name        = "replicate-vfs"
  s.version     = ENV.fetch("REPLICATE_VERSION", "0.0.0")
  s.summary     = "Replicate VFS extension for SQLite"
  s.description = "Bundles the Replicate VFS shared library for loading into SQLite connections."
  s.homepage    = "https://github.com/hanzoai/replicate"
  s.license     = "Apache-2.0"
  s.authors     = ["Hanzo AI"]

  s.platform = Gem::Platform.new(ENV.fetch("PLATFORM", RUBY_PLATFORM))

  s.files = Dir["lib/**/*.rb"] + Dir["lib/**/*.so"] + Dir["lib/**/*.dylib"]
  s.require_paths = ["lib"]

  s.required_ruby_version = ">= 2.7"
end
