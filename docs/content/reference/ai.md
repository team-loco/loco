---
title: "Use with AI"
---

Loco's documentation includes Markdown exports for AI assistants and tools that ingest text. The exports come from the same pages as the rendered documentation.

To have an agent deploy your application, give it: `Deploy my app to Loco: https://loco.build/skills.md`.

```sh
curl -fsSL https://docs.loco.build/llms.txt
curl -fsSL https://docs.loco.build/llms-full.txt
curl -fsSL https://loco.build/skills.md
```

## Choose an export

| Export | Use |
| --- | --- |
| [`llms.txt`](../llms.txt) | Find pages by section and follow their Markdown links |
| [`llms-full.txt`](../llms-full.txt) | Load the complete published documentation as text |
| [`skills.md`](../skills.md) | Guide an agent through deploying an application to Loco |
| View Markdown | Open the current page as Markdown |

Use View Markdown below the title to open the source. Page-specific Markdown URLs appear in `llms.txt`.

## Match the release

Ask the assistant to check the installed CLI's help before generating commands. Pages describe the main branch, which can be ahead of your release.

The build exports these files from the published Markdown sources. They require no AI account or API key.
