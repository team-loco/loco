<p align="center">
  <img src="web/public/logo.svg" alt="Loco" width="240">
</p>

Loco builds, deploys, and scales containerized applications on Kubernetes. Use the `loco` CLI to deploy from your terminal and the dashboard to inspect what is running.

## Quick start

Install the CLI on Linux or macOS (amd64 or arm64), sign in, and deploy the application in the current directory. The directory needs a `Dockerfile`.

```sh
curl -fsSL https://loco.build/install.sh | sh
loco login
loco deploy --name web --port 8080 --yes
```

Run `loco help` to list the commands your installed release supports.

## Links

- [Documentation](https://docs.loco.build/)
- [Self-hosting](https://docs.loco.build/operations/self-hosting/)
- [Contributing](docs/content/contributing/development.md): install [mise](https://mise.jdx.dev/getting-started.html), then run `mise run setup`

## For agents

- [`AGENTS.md`](AGENTS.md): repository layout, commands, and rules for contributors
- [`skills.md`](https://loco.build/skills.md): instructions for deploying an application to Loco
- [`llms.txt`](https://docs.loco.build/llms.txt) and [`llms-full.txt`](https://docs.loco.build/llms-full.txt): the documentation as Markdown

## License

Loco is released under the [MIT License](LICENSE).
