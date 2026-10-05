import { FOOTER_COLUMNS } from "./content";
import { LocoLogo } from "@/components/design/LocoLogo";

const COPYRIGHT_YEAR = new Date().getFullYear();

export function SplashFooter() {
	return (
		<footer className="border-t border-line px-4 pt-10 pb-6 md:px-8">
			<div className="mx-auto mb-8 grid max-w-[1200px] gap-10 sm:grid-cols-2 lg:grid-cols-4">
				<div className="flex flex-col gap-2.5 sm:col-span-2 lg:col-span-1">
					<div className="flex items-center gap-2">
						<LocoLogo className="w-16" />
					</div>
					<p className="m-0 text-fg3">Modern infrastructure for a modern world.</p>
				</div>
				{FOOTER_COLUMNS.map((column) => (
					<div key={column.title} className="flex flex-col gap-3">
						<h4 className="m-0 text-xs font-semibold tracking-[0.08em] text-fg3 uppercase">{column.title}</h4>
						<ul className="m-0 flex list-none flex-col gap-2 p-0">
							{column.links.map(({ label, href }) => {
								const external = href.startsWith("http");
								return (
									<li key={label}>
										<a
											href={href}
											target={external ? "_blank" : undefined}
											rel={external ? "noreferrer" : undefined}
											className="text-fg2 transition-colors hover:text-foreground"
										>
											{label}
										</a>
									</li>
								);
							})}
						</ul>
					</div>
				))}
			</div>
			<div className="mx-auto flex max-w-[1200px] flex-col items-center justify-between gap-2 border-t border-line pt-5 text-sm text-fg3 sm:flex-row">
				<span>© {COPYRIGHT_YEAR} Loco. All rights reserved.</span>
				<span>Built for developers by developers</span>
			</div>
		</footer>
	);
}
