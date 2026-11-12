import { TextAttributes, type TextareaRenderable } from "@opentui/core"
import { useTerminalDimensions } from "@opentui/solid"
import { createMemo, createSignal, onMount } from "solid-js"
import type { Context } from "@opencode/plugin/tui/context"
import type { CommentPrompt } from "./command.ts"
import { commentHeader, commentLayout, commentLineCount } from "./format.ts"

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
        const [lines, setLines] = createSignal(1)
        const layout = createMemo(() => commentLayout(dimensions().width, dimensions().height, lines()))
        const header = createMemo(() => commentHeader(options.sourceTitle, options.destinationTitle, options.provider, options.directory, layout().width))
        const resizeField = () => setLines(commentLineCount(input?.plainText ?? "", layout().width - 2))
        const theme = context.theme
        onMount(() => input.focus())
        context.keymap.layer(() => ({ mode: "global", target: () => input, priority: 1, commands: [
          { bind: "return", run: () => finish(input.plainText, true) },
          { bind: "escape", run: () => finish(undefined, true) },
          { bind: "ctrl+c", run: () => finish(undefined, true) },
        ] }))
        return <box paddingTop={1} paddingBottom={1} paddingLeft={2} paddingRight={2} gap={1}>
          <box>
            <text fg={theme.text.base} attributes={TextAttributes.BOLD} wrapMode="none">{header().title}</text>
            <text fg={theme.text.muted} wrapMode="none">{header().source}</text>
          </box>
          {/* Native dialogs use the panel/raised.base surface; the field uses
              element/high, with max on focus, in both dark and light themes. */}
          <box backgroundColor={theme.background.raised.high} padding={1}>
            <textarea ref={node => { input = node }} placeholder={options.placeholder}
              height={layout().fieldRows} minHeight={layout().fieldRows} maxHeight={layout().maxRows} flexShrink={0} wrapMode="word"
              onContentChange={resizeField} onCursorChange={resizeField}
              textColor={theme.text.base} backgroundColor={theme.background.raised.high}
              focusedBackgroundColor={theme.background.raised.max} placeholderColor={theme.text.muted}
              cursorColor={theme.text.base}
              keyBindings={[{ name: "return", action: "submit" }, { name: "return", shift: true, action: "newline" }]}
              onSubmit={() => finish(input.plainText, true)} />
          </box>
          <box flexDirection="row" flexWrap="wrap" gap={2}>
            <text fg={theme.text.action.primary.selected} onMouseUp={() => finish(input.plainText, true)}>enter send</text>
            <text fg={theme.text.muted}>shift+enter newline</text>
            <text fg={theme.text.muted} onMouseUp={() => finish(undefined, true)}>esc cancel</text>
          </box>
        </box>
      }, () => finish())
      context.ui.dialog.set({ size: "large", centered: false })
      signal.addEventListener("abort", abort, { once: true })
    })
  }
}
