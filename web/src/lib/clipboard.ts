/**
 * Copies `text` to the clipboard and says whether it worked.
 *
 * `navigator.clipboard` only exists in secure contexts, and the demo serves
 * the app over plain HTTP on the tailnet, so fall back to selecting a hidden
 * textarea and `execCommand("copy")`. Callers should still show the text, so a
 * person can select it by hand when both fail.
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (globalThis.isSecureContext && navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Permission denied or no focus: try the fallback.
  }
  return copyWithSelection(text);
}

function copyWithSelection(text: string): boolean {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.top = "0";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
  }
}
