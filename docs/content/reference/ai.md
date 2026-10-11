# Use with AI

Loco's documentation includes Markdown exports for AI assistants and tools that ingest text. The exports come from the same pages as the rendered documentation.

```sh
curl -fsSL https://docs.loco.build/llms.txt
curl -fsSL https://docs.loco.build/llms-full.txt
```

## Choose an export

| Export | Use |
| --- | --- |
| [`llms.txt`](../llms.txt) | Find pages by section and follow their Markdown links |
| [`llms-full.txt`](../llms-full.txt) | Load the complete published documentation as text |
| Copy as Markdown | Copy the current page into an assistant |

Use the page action beside the title to copy Markdown. Page-specific Markdown URLs appear in `llms.txt`.

## Match the release

Ask the assistant to check the installed CLI's help before generating commands. Pages describe the main branch, which can be ahead of your release.

These helpers use [Zensical's native `llmstxt` support](https://zensical.org/docs/compatibility/mkdocs/plugins/#llmstxt). They do not require an AI account or an API key. Copying a page uses your browser's clipboard; these docs do not send it to an AI provider.
