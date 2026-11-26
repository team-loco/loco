import type { LanguageName } from "sugar-high";

import { CodeBlock } from "@/components/design/CodeBlock";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/design/Tabs";

const SNIPPETS: { language: string; highlight: LanguageName; code: string }[] = [
	{
		language: "Node",
		highlight: "shell",
		code: [
			"npm install @opentelemetry/auto-instrumentations-node",
			"node --require @opentelemetry/auto-instrumentations-node/register app.js",
		].join("\n"),
	},
	{
		language: "Python",
		highlight: "shell",
		code: [
			"pip install opentelemetry-distro opentelemetry-exporter-otlp",
			"opentelemetry-bootstrap -a install",
			"opentelemetry-instrument python app.py",
		].join("\n"),
	},
	{
		language: "Go",
		highlight: "go",
		code: [
			"go get go.opentelemetry.io/otel/sdk",
			"go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp",
			"go get go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp",
			"",
			"// main.go",
			"exporter, err := otlptracehttp.New(ctx)",
			"otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter)))",
			'http.ListenAndServe(":8080", otelhttp.NewHandler(mux, "server"))',
		].join("\n"),
	},
	{
		language: "Java",
		highlight: "shell",
		code: [
			"curl -LO https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases/latest/download/opentelemetry-javaagent.jar",
			"java -javaagent:opentelemetry-javaagent.jar -jar app.jar",
		].join("\n"),
	},
];

export function TraceSetup() {
	return (
		<Tabs
			defaultValue={SNIPPETS[0]?.language}
			className="w-[min(760px,100%)] overflow-hidden rounded-lg border border-line2 bg-background text-left"
		>
			<TabsList aria-label="Language" className="px-2">
				{SNIPPETS.map((s) => (
					<TabsTrigger key={s.language} value={s.language} className="text-[12.5px]">
						{s.language}
					</TabsTrigger>
				))}
			</TabsList>
			{SNIPPETS.map((s) => (
				<TabsContent key={s.language} value={s.language}>
					<CodeBlock language={s.highlight} wrap className="rounded-none border-0">
						{s.code}
					</CodeBlock>
				</TabsContent>
			))}
		</Tabs>
	);
}
