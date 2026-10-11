import type { EngineInterface, Register } from 'claude-code'

// The pole as `jalon statusline` draws it: the binary keeps the rule (the
// session's starting size, advise_tokens), the mod only draws its answer.
type Pole = { filled: number; size: string; isAdvised: boolean }

const poleLength = 3
const poleRed = '#E8381B'

let binary = 'jalon'
let pole: Pole | null = null

export const register: Register = (on, options) => {
  binary = typeof options.binary === 'string' && options.binary !== '' ? options.binary : 'jalon'

  // A resumed session, or a reload, has a size before its next turn.
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    await measure($, (await $.session.usage()).context.tokens)
    return result
  })

  on('session.measure', async ($, e, next) => {
    if (e.changed.includes('context')) {
      await measure($, e.context.tokens)
    }
    return next(e)
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (pole === null || e.props.hasSurvey) {
      return next(e)
    }
    const { Box, Text } = $.ui.resolve(e)
    return (
      <Box>
        <Text dimColor>jalon </Text>
        <Text color={poleRed}>{'▰'.repeat(pole.filled)}</Text>
        <Text dimColor>{'▱'.repeat(poleLength - pole.filled)}</Text>
        <Text dimColor> {pole.size}</Text>
        {pole.isAdvised ? <Text color={poleRed}> · reset advised</Text> : null}
      </Box>
    )
  })
}

// measure asks the binary for the pole at this size, with the input its status
// line reads, and redraws. The pole is cleared when there is no size, when the
// binary is missing or fails, and when the advice is off (no pole printed). The
// binary is not run when nothing draws the band, as in the VS Code chat panel.
async function measure($: EngineInterface, tokens: number | undefined) {
  pole = null
  const isDrawn = (await $.session.surfaces()).some(s => s === 'terminal' || s === 'desktop')
  if (isDrawn && tokens !== undefined && tokens > 0) {
    const input = JSON.stringify({
      session_id: await $.session.id(),
      context_window: { total_input_tokens: tokens },
    })
    try {
      const { exitCode, stdout } = await $.process.run([binary, 'statusline', '--client', 'claude-code'], {
        stdin: input,
        env: { NO_COLOR: '1' },
        timeoutMs: 2000,
      })
      if (exitCode === 0) {
        pole = parsePole(stdout)
      }
    } catch {
      // The binary is missing or hung: draw nothing, as the status line does.
    }
  }
  $.ui.invalidate('ui.render')
}

// parsePole reads `jalon ▰▰▱ 230K · reset advised` without color.
export function parsePole(line: string): Pole | null {
  const match = /^jalon ([▰▱]{3}) (\d+K)( · reset advised)?$/.exec(line.trim())
  if (match === null) {
    return null
  }
  const [, bands = '', size = '', advice] = match
  return { filled: [...bands].filter(c => c === '▰').length, size, isAdvised: advice !== undefined }
}
