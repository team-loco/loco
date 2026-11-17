import { ART_SOFT, ART_STRONG, ClusterArt } from "@/components/design/ClusterArt";
import "./TokensArt.css";

export function TokensArt() {
	return (
		<ClusterArt className="es-tokens">
			<circle className="es-wire" r={2.2} fill="var(--info)" />
			<rect x={142} y={30} width={40} height={4} rx={2} fill={ART_SOFT} />
			<rect x={142} y={48} width={82} height={20} rx={4} fill="var(--panel)" stroke="var(--line2)" />
			{Array.from({ length: 11 }, (_, i) => (
				<circle
					key={i}
					className="es-dot"
					style={{ animationDelay: `${i * 0.18}s` }}
					cx={150 + i * 7}
					cy={58}
					r={1.8}
					fill={ART_STRONG}
				/>
			))}
			<g className="es-ok">
				<rect x={142} y={78} width={30} height={12} rx={3} fill="color-mix(in oklch, var(--info) 16%, var(--bg))" />
				<rect x={147} y={82.5} width={20} height={3} rx={1.5} fill="var(--info)" />
			</g>
		</ClusterArt>
	);
}
