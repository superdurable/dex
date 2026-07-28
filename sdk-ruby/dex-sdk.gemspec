# frozen_string_literal: true

require_relative "lib/dex/sdk/version"

Gem::Specification.new do |spec|
  spec.name          = "dex-sdk"
  spec.version       = Dex::Sdk::VERSION
  spec.authors       = ["SuperDurable"]
  spec.email         = ["hello@superdurable.io"]

  spec.summary       = "Placeholder for the Dex workflow SDK (Ruby)"
  spec.description   = "Name reservation for dex-sdk on RubyGems. Not for production use yet."
  spec.homepage      = "https://github.com/superdurable/dex/tree/main/sdk-ruby"
  spec.license       = "Apache-2.0"
  spec.required_ruby_version = ">= 2.7.0"

  spec.metadata["homepage_uri"] = spec.homepage
  spec.metadata["source_code_uri"] = "https://github.com/superdurable/dex"

  spec.files = [
    "README.md",
    "dex-sdk.gemspec",
    "lib/dex-sdk.rb",
    "lib/dex/sdk.rb",
    "lib/dex/sdk/version.rb",
  ]
  spec.require_paths = ["lib"]
end
