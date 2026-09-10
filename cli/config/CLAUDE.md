# cli/config

## Gotchas

- **The credentials file is shared with knife.** It holds keys this CLI has no
  model for, whether knife-only settings or something a user hand-added.
  Anything that rewrites it must merge, not re-serialize a struct, or those keys
  are silently dropped. `WriteProfile` is where this matters.

The on-disk format, every supported key, and the chef-compat pairs are
documented in `docs/configuration.md`, which is the source of truth.
