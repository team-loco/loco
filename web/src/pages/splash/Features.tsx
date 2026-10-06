import { FeatureGlyph, type GlyphKind } from "./FeatureGlyph";
import { CornerMarks, Frame, SectionHeading } from "./primitives";

const FEATURES: readonly { glyph: GlyphKind; title: string; body: string }[] = [
	{
		glyph: "tls",
		title: "HTTPS by default",
		body: "Every service gets a certificate on deploy, renewed before it expires.",
	},
	{
		glyph: "quic",
		title: "HTTP/3 at the edge",
		body: "Envoy terminates TLS, speaks QUIC and routes by host and path.",
	},
	{
		glyph: "roll",
		title: "Zero-downtime rollouts",
		body: "New replicas must pass health checks before old ones drain. One-click rollback.",
	},
	{
		glyph: "scale",
		title: "Autoscaling",
		body: "Set a replica range and a CPU target per region.",
	},
	{
		glyph: "obs",
		title: "Logs and metrics",
		body: "Collected from every replica, searchable and charted in the dashboard.",
	},
	{
		glyph: "token",
		title: "Granular tokens",
		body: "Give CI write on one service and nothing else. Expire in 30 days or less.",
	},
];

export function Features() {
	return (
		<section className="border-t border-line bg-bg2">
			<Frame className="flex flex-col gap-12 py-28">
				<CornerMarks />
				<SectionHeading index="02" eyebrow="Included" title="The platform work, already done." />
				<div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,320px),1fr))] border-t border-l border-line">
					{FEATURES.map(({ glyph, title, body }) => (
						<div key={title} className="flex flex-col gap-2.5 border-r border-b border-line bg-background p-7">
							<span className="mb-1.5 flex h-14">
								<FeatureGlyph kind={glyph} />
							</span>
							<span className="font-semibold">{title}</span>
							<span className="text-[14.5px] text-pretty text-fg2">{body}</span>
						</div>
					))}
				</div>
			</Frame>
		</section>
	);
}
