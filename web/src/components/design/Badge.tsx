import { mergeProps } from "@base-ui/react/merge-props"
import { useRender } from "@base-ui/react/use-render"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

const badgeVariants = cva(
  "inline-flex h-5 w-fit shrink-0 items-center justify-center gap-1 overflow-hidden rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap transition-colors [&>svg]:pointer-events-none [&>svg]:size-3!",
  {
    variants: {
      variant: {
        default: "bg-secondary text-secondary-foreground border-border",
        secondary: "bg-secondary text-secondary-foreground border-border",
        primary: "bg-primary text-primary-foreground border-transparent",
        success:
          "border-[#b8d4bf] bg-[#eef3f1] text-[#3a7a4a] dark:border-[#2d4d35] dark:bg-[#1a2e20] dark:text-[#6abf7a]",
        warning:
          "border-[#e5dccf] bg-[#f9f6f0] text-[#b07a10] dark:border-[#4a3d20] dark:bg-[#2a2210] dark:text-[#d4a030]",
        error:
          "border-[#f5b8b2] bg-[#fdecea] text-[#d92818] dark:border-[#5c2020] dark:bg-[#2a1010] dark:text-[#e8391d]",
        info: "border-[#c9d6e2] bg-[#f0f4f7] text-[#6b8aaa] dark:border-[#253545] dark:bg-[#151e28] dark:text-[#8aaac8]",
        running:
          "border-[#b8d4bf] bg-[#eef3f1] text-[#3a7a4a] dark:border-[#2d4d35] dark:bg-[#1a2e20] dark:text-[#6abf7a]",
        pending:
          "border-[#e5dccf] bg-[#f9f6f0] text-[#b07a10] dark:border-[#4a3d20] dark:bg-[#2a2210] dark:text-[#d4a030]",
        stopped:
          "border-border bg-muted text-muted-foreground",
        destructive:
          "border-[#f5b8b2] bg-[#fdecea] text-[#d92818] dark:border-[#5c2020] dark:bg-[#2a1010] dark:text-[#e8391d]",
        outline: "border-border text-foreground bg-transparent",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

function Badge({
  className,
  variant = "default",
  render,
  ...props
}: useRender.ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return useRender({
    defaultTagName: "span",
    props: mergeProps<"span">(
      {
        className: cn(badgeVariants({ variant }), className),
      },
      props
    ),
    render,
    state: {
      slot: "badge",
      variant,
    },
  })
}

export { Badge, badgeVariants }
