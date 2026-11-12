import type { TextareaRenderable } from "@opentui/core"
import { useTerminalDimensions } from "@opentui/solid"
import { onMount } from "solid-js"
import type { Context } from "@opencode/plugin/tui/context"
import type { CommentPrompt } from "./command.ts"

// Native dialog.prompt is a one-line textarea. This dialog intentionally owns
// multiline input and the single send/cancel boundary; it never calls the API.
export function commentPrompt(context: Context): CommentPrompt {
  return (options, signal) => {
    signal.throwIfAborted()
    return new Promise<string | undefined>(resolve => {
      let settled = false
      const finish = (value?: string, close = false) => {
        if (settled) return
        settled = true
        signal.removeEventListener("abort", abort)
        resolve(value)
        if (close) context.ui.dialog.clear()
      }
      const abort = () => finish(undefined, true)
      context.ui.dialog.show(() => {
        let input!: TextareaRenderable
        const dimensions = useTerminalDimensions()
        const height = () => Math.max(1, Math.min(18, dimensions().height - 14))
        const theme = context.theme
        onMount(() => input.focus())
        context.keymap.layer(() => ({ mode: "global", target: () => input, priority: 1, commands: [
          { bind: "return", run: () => finish(input.plainText, true) },
          { bind: "escape", run: () => finish(undefined, true) },
          { bind: "ctrl+c", run: () => finish(undefined, true) },
        ] }))
        return <box paddingLeft={2} paddingRight={2} paddingBottom={1} gap={1}>
          <text fg={theme.text.base}>{options.title}</text>
          <box backgroundColor={theme.background.raised.high} paddingLeft={1} paddingRight={1}>
            <textarea ref={node => { input = node }} placeholder={options.placeholder}
              height="auto" minHeight={Math.min(3, height())} maxHeight={height()} flexShrink={1} wrapMode="word"
              textColor={theme.text.base} backgroundColor={theme.background.raised.high}
              focusedBackgroundColor={theme.background.raised.high} placeholderColor={theme.text.muted}
              cursorColor={theme.text.base}
              keyBindings={[{ name: "return", action: "submit" }, { name: "return", shift: true, action: "newline" }]}
              onSubmit={() => finish(input.plainText, true)} />
          </box>
          <text fg={theme.text.muted}>enter send · shift+enter newline · esc cancel</text>
        </box>
      }, () => finish())
      context.ui.dialog.set({ size: "xlarge" })
      signal.addEventListener("abort", abort, { once: true })
    })
  }
}
