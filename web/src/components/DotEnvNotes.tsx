export function DotEnvNotes({ notes }: { notes: string[] }) {
	if (notes.length === 0) return null;
	return (
		<ul aria-label="Import notes" className="m-0 flex list-none flex-col gap-0.5 p-0 text-[12.5px] text-warn">
			{notes.map((note) => (
				<li key={note}>{note}</li>
			))}
		</ul>
	);
}
