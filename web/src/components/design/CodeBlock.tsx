import { CodeBlock as CodeBlockBase, type CodeBlockProps } from "@/components/ui/code-block"
import { cn } from "@/lib/utils"

function CodeBlock({ className, ...props }: CodeBlockProps) {
	return (
		<CodeBlockBase
			className={cn(
				"rounded-lg border-line bg-background shadow-none [&>div:first-child]:border-line [&>div:first-child]:bg-bg2 [&>div:first-child>span]:text-fg2 [&>div:first-child>button]:text-fg3 [&>div:first-child>button:hover]:text-foreground [&_pre]:text-foreground",
				className
			)}
			{...props}
		/>
	)
}

export { CodeBlock, type CodeBlockProps }
