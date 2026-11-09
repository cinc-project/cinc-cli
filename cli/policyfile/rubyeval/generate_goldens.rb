#!/usr/bin/env ruby
# frozen_string_literal: false
#
# generate_goldens.rb — produce golden expected.json files for the Policyfile
# evaluation corpus by driving the REAL chef-cli Policyfile DSL.
#
# This is what makes the chef-compatibility claim real: every fixture under
# testdata/<name>/Policyfile.rb is evaluated with chef-cli's own
# ChefCLI::Policyfile::DSL (constraint normalization via the real Semverse gem,
# attribute deep-merge via the real Chef::Node::Attribute, run_list validation,
# error collection) and the captured state is serialized to the same canonical
# JSON schema the Go engine's shim.rb emits. The Go test then diffs the engine's
# output (CRuby-in-wasm + our shim) against these goldens.
#
# Requires the chef-cli gem:
#     gem install chef-cli
#     ruby cli/policyfile/rubyeval/generate_goldens.rb
#
# Fidelity notes / honest boundaries:
#   * Every field except default_source is read directly from chef's evaluated
#     DSL object.
#   * default_source is serialized from the raw declaration (the type + argument
#     the user wrote), expanding :chef_repo paths against a fixed sentinel root
#     so goldens are portable across machines. This is byte-identical to what
#     the shim does and faithful to the user's intent; we avoid baking a
#     machine-specific absolute path into the goldens.
#   * The eval filename is the real fixture path so require_relative / File.read
#     of sibling files resolve, while :chef_repo expansion uses the sentinel —
#     the two are intentionally decoupled.

require "json"
require "chef-cli/policyfile/dsl"
require "chef-cli/policyfile/storage_config"

# Must match cli/policyfile/rubyeval/rubyeval.go's policyfileRoot and shim.rb.
POLICYFILE_ROOT = "/cinc/policyfile".freeze
COMMUNITY_DEFAULT_URI = "https://supermarket.chef.io".freeze

# CaptureDSL is the real chef DSL with raw default_source declarations recorded
# so we can serialize them portably (see fidelity notes above).
class CaptureDSL < ChefCLI::Policyfile::DSL
  attr_reader :raw_default_sources

  def initialize(*args, **kwargs)
    @raw_default_sources = []
    super
  end

  def default_source(source_type = nil, source_argument = nil, &block)
    @raw_default_sources << [source_type, source_argument] unless source_type.nil?
    super
  end
end

def serialize_default_source(type, arg)
  case type
  when :community, :supermarket
    { "type" => "community", "uri" => arg || COMMUNITY_DEFAULT_URI }
  when :delivery_supermarket
    arg.nil? ? nil : { "type" => "delivery_supermarket", "uri" => arg }
  when :chef_server
    arg.nil? ? nil : { "type" => "chef_server", "uri" => arg }
  when :chef_repo
    arg.nil? ? nil : { "type" => "chef_repo", "path" => File.expand_path(arg, POLICYFILE_ROOT) }
  when :artifactory
    arg.nil? ? nil : { "type" => "artifactory", "uri" => arg }
  else
    nil
  end
end

def stringify_options(opts)
  out = {}
  opts.each { |k, v| out[k.is_a?(Symbol) ? k.to_s : k] = v }
  out
end

def canonical(dsl)
  cookbooks = {}
  dsl.cookbook_location_specs.each do |name, spec|
    cookbooks[name] = {
      "version_constraint" => spec.version_constraint.to_s,
      "source_options" => stringify_options(spec.source_options),
    }
  end

  named = {}
  dsl.named_run_lists.each { |k, v| named[k.to_s] = v }

  default_sources = dsl.raw_default_sources.map { |t, a| serialize_default_source(t, a) }.compact

  {
    "name" => dsl.name,
    "run_list" => dsl.run_list,
    "named_run_lists" => named,
    "default_source" => default_sources,
    "cookbooks" => cookbooks,
    "included_policies" => dsl.included_policies.map { |p| { "name" => p.name, "source_options" => stringify_options(p.source_options) } },
    "default_attributes" => dsl.node_attributes.default.to_hash,
    "override_attributes" => dsl.node_attributes.override.to_hash,
    "errors" => dsl.errors,
  }
end

def evaluate(policyfile_path)
  sc = ChefCLI::Policyfile::StorageConfig.new
  # relative_paths_root drives :chef_repo expansion; we override that with the
  # sentinel in serialize_default_source, so the value here only needs to be
  # stable. We point use_policyfile at the real file so require_relative works.
  sc.use_policyfile(policyfile_path)
  dsl = CaptureDSL.new(sc, chef_config: nil)
  dsl.eval_policyfile(File.read(policyfile_path))
  result = canonical(dsl)
  # Error messages from chef can embed the absolute path of the Policyfile (in
  # syntax-error and raised-exception backtraces). Redact it to a stable token
  # so committed goldens are portable. The Go test compares these error strings
  # by count plus a substring check, not byte-for-byte (the shim's wording for
  # raise/syntax differs from chef's verbose backtrace formatting on purpose).
  result["errors"] = result["errors"].map { |e| e.gsub(policyfile_path, "Policyfile.rb").gsub(File.dirname(policyfile_path), ".") }
  result
end

# confined_fixture resolves a globbed fixture path (following symlinks) and
# refuses it unless it is a Policyfile.rb directly inside a testdata/<name>/
# directory. Every path we read from or write to comes from here, so a
# symlinked fixture can't make the generator read or overwrite a file outside
# the corpus.
def confined_fixture(path, root)
  real = File.realpath(path)
  dir = File.dirname(real)
  unless File.basename(real) == "Policyfile.rb" && File.dirname(dir) == root
    abort "refusing fixture #{path}: it resolves to #{real}, outside #{root}"
  end
  real
end

testdata = File.realpath(File.join(__dir__, "testdata"))
fixtures = Dir.glob(File.join(testdata, "*", "Policyfile.rb")).sort.map { |pf| confined_fixture(pf, testdata) }
if fixtures.empty?
  warn "no fixtures found under #{testdata}"
  exit 1
end

fixtures.each do |pf|
  dir = File.dirname(pf)
  name = File.basename(dir)
  result = evaluate(pf)
  out = File.join(dir, "expected.json")
  File.write(out, JSON.pretty_generate(result) + "\n")
  errs = result["errors"].length
  puts "#{name.ljust(34)} -> expected.json (errors: #{errs})"
end

puts "\nGenerated #{fixtures.length} goldens from chef-cli #{Gem.loaded_specs["chef-cli"]&.version}."
