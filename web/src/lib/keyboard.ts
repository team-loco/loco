const IS_MAC = /Mac|iPhone|iPad/.test(navigator.userAgent);

export const modifierKey = IS_MAC ? "⌘" : "Ctrl";
export const shortcutLabel = (key: string) => (IS_MAC ? `⌘${key}` : `Ctrl ${key}`);
