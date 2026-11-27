import { useStoredValue } from "./storage";

export function useLogPreferences() {
	const [parsedJson, setParsedJson] = useStoredValue("loco:logs:parsed-json", "off");
	return {
		parsedJson: parsedJson === "on",
		setParsedJson: (enabled: boolean) => setParsedJson(enabled ? "on" : "off"),
	};
}
