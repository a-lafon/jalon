import type { On, RenderSurface } from 'claude-code'
import { expect, test } from 'claude-code/testing'

const props = {
  hasSurvey: false,
  isWorking: false,
  maxRows: 4,
  bodyColumns: 80,
  scroll: { offset: 0, bodyRows: 4 },
  view: {},
}

// ran is what $.process.run resolves with for a run that printed stdout.
function ran(stdout: string, exitCode = 0) {
  return { value: { exitCode, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
}

// session answers, beneath the plugin, what the engine would: the surfaces it
// draws on, its id, the end of a measurement, and an empty band above the prompt.
function session(on: On, surfaces: RenderSurface[] = ['terminal']) {
  on('session.surfaces', () => ({ value: surfaces }))
  on('ui.render', { component: 'AbovePrompt' }, ($, e) => {
    const { Box } = $.ui.resolve(e)
    return <Box />
  })
  on('session.id', () => ({ value: 'b7c1e2d4-0000-4000-8000-000000000001' }))
  on('session.measure', (_$, e) => ({ changed: e.changed }))
}

// The text of a drawn tree, its strings joined in order.
function text(node: unknown): string {
  if (typeof node === 'string' || typeof node === 'number') {
    return String(node)
  }
  if (Array.isArray(node)) {
    return node.map(text).join('')
  }
  if (node !== null && typeof node === 'object') {
    const { props, children } = node as { props?: { children?: unknown }; children?: unknown }
    return text(children ?? props?.children ?? '')
  }
  return ''
}

for (const surface of ['terminal', 'desktop'] as const) {
  test(`${surface}: draws the binary's pole and its advice`, async ($, on) => {
    session(on, [surface])
    const runs: { argv: readonly string[]; stdin?: string }[] = []
    on('process.run', (_$, e) => {
      runs.push({ argv: e.argv, stdin: e.init?.stdin })
      return ran('jalon ▰▰▰ 230K · reset advised\n')
    })

    await $.session.measure({ context: { tokens: 230_000, window: 1_000_000 }, rateLimits: [], changed: ['context'] })

    expect(runs.at(-1)?.argv).toEqual(['jalon', 'statusline', '--client', 'claude-code'])
    expect(JSON.parse(runs.at(-1)?.stdin ?? '{}').context_window).toEqual({ total_input_tokens: 230_000 })
    const mounted = await $.ui.mount({ plugin: 'jalon', surface, component: 'AbovePrompt', props })
    expect(text(await mounted.drawn())).toBe('jalon ▰▰▰ 230K · reset advised')
  })

  test(`${surface}: draws nothing when the binary fails`, async ($, on) => {
    session(on)
    on('process.run', () => ran('', 1))

    await $.session.measure({ context: { tokens: 50_000, window: 1_000_000 }, rateLimits: [], changed: ['context'] })

    const mounted = await $.ui.mount({ plugin: 'jalon', surface, component: 'AbovePrompt', props })
    expect(text(await mounted.drawn())).toBe('')
  })
}

// The text of each drawn node that has a color or dimColor prop, in order.
function colored(node: unknown, out: string[] = []): string[] {
  if (Array.isArray(node)) {
    node.forEach(n => colored(n, out))
  } else if (node !== null && typeof node === 'object') {
    const { props, children } = node as { props?: { color?: string; dimColor?: boolean; children?: unknown }; children?: unknown }
    if (props?.color !== undefined || props?.dimColor === true) {
      out.push(`${props.color ?? 'dim'}:${text(children ?? props.children ?? '')}`)
    } else {
      colored(children ?? props?.children, out)
    }
  }
  return out
}

test('desktop: draws every band with the same glyph, filled ones in red', async ($, on) => {
  session(on, ['desktop'])
  on('process.run', () => ran('jalon ▰▱▱ 80K\n'))

  await $.session.measure({ context: { tokens: 80_000, window: 1_000_000 }, rateLimits: [], changed: ['context'] })

  const drawn = await (await $.ui.mount({ plugin: 'jalon', surface: 'desktop', component: 'AbovePrompt', props })).drawn()
  expect(text(drawn)).toBe('jalon ▰▰▰ 80K')
  expect(colored(drawn).slice(1, 3)).toEqual(['#E8381B:▰', 'dim:▰▰'])
})

test('uses the configured binary', { options: { binary: '/opt/bin/jalon' } }, async ($, on) => {
  session(on)
  const argv: (string | undefined)[] = []
  on('process.run', (_$, e) => {
    argv.push(e.argv[0])
    return ran('jalon ▰▱▱ 80K\n')
  })

  await $.session.measure({ context: { tokens: 80_000, window: 1_000_000 }, rateLimits: [], changed: ['context'] })

  expect(argv.at(-1)).toBe('/opt/bin/jalon')
})

test('runs nothing where no surface draws the band', async ($, on) => {
  session(on, ['vscode'])
  const argv: (string | undefined)[] = []
  on('process.run', (_$, e) => {
    argv.push(e.argv[0])
    return ran('jalon ▰▱▱ 80K\n')
  })

  await $.session.measure({ context: { tokens: 80_000, window: 1_000_000 }, rateLimits: [], changed: ['context'] })

  expect(argv).toEqual([])
})
