# Community

ClawDE is open source and welcomes contributions.

## Get Involved

| Channel | Purpose |
| --- | --- |
| [GitHub Issues](https://github.com/nself-org/clawde/issues) | Bug reports, feature requests |
| [GitHub Discussions](https://github.com/nself-org/clawde/discussions) | Questions, ideas, show and tell |
| [Discord](https://discord.gg/clawde) | Real-time chat, `#dev` for contributors |
| [clawde.io/packs](https://clawde.io/packs) | Pack showcase — browse community packs |

## Contributing Code

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the full guide. Short version:

1. Fork → branch → code → `cargo test && flutter test`
2. Submit PR — maintainers review within a few days
3. Merged contributors appear on the contributor wall at [clawde.io](https://clawde.io)

## Publishing Packs

Build and publish packs to extend ClawDE:

```bash
# Create a pack
clawd pack init my-pack

# Publish to registry
clawd pack publish
```

See the [Pack Author Guide](https://base.clawde.io/docs/marketplace-author-guide) for the full workflow, including paid packs and Stripe Connect payouts.

## Pack Ratings

Rate an installed pack (1–5 stars):

```bash
clawd pack rate my-pack 5
```

Ratings feed into the trending algorithm on the pack showcase page.

## Code of Conduct

All participants must follow our [Code of Conduct](../CODE_OF_CONDUCT.md). Reports go to conduct@clawde.io.

## Security

Report vulnerabilities privately — see [SECURITY.md](../SECURITY.md). Do not open public issues for security bugs.

## Sponsorship

ClawDE is free and open source. If it saves you time, consider [sponsoring the project](https://github.com/sponsors/clawde-io).
