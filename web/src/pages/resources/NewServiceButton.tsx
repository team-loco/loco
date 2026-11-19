import { PlusIcon } from "lucide-react";
import { Link } from "react-router";

import { Button } from "@/components/design/Button";

export function NewServiceButton({ serviceHref, disabled }: { serviceHref: string; disabled: boolean }) {
	if (disabled) {
		return (
			<Button disabled>
				<PlusIcon />
				New service
			</Button>
		);
	}
	return (
		<Button nativeButton={false} render={<Link to={serviceHref} />}>
			<PlusIcon />
			New service
		</Button>
	);
}
