import { createHash, randomUUID } from 'node:crypto'
import z from '@deepseek-ai/schemastery'
import { installModelSelection } from '@deepseek-ai/dsh-agent'
import { freezeMessage, MessageId } from '@deepseek-ai/dsh-llm'
import { SessionId } from '@deepseek-ai/dsh-session'


export const name = 'agentteams-headless-runner'
export const inject = ['agentDefaultModel', 'agents', 'sessions', 'sessionPersistence']

export const Config = z.object({
  task: z.string().required(),
  sessionId: z.string().default(''),
  resume: z.boolean().default(false),
  eventId: z.string().default(''),
})

function summarize(events, firstSeq) {
  let started = false
  let text = ''
  let reason
  for (const event of events) {
    if (event.seq < firstSeq) continue
    if (event.type === 'turn/start') {
      started = true
      continue
    }
    if (!started) continue
    if (event.type === 'assistant/message') {
      const joined = event.data.message.content
        .filter(block => block.type === 'text')
        .map(block => block.text)
        .join('')
      if (joined !== '') text = joined
    }
    if (event.type === 'turn/end') {
      reason = event.data.reason
      break
    }
  }
  return { text, reason }
}

function messageId(eventId) {
  if (!eventId) return MessageId(randomUUID())
  const digest = createHash('sha256').update(eventId).digest('hex')
  return MessageId(`matrix-${digest}`)
}

function userMessage(task, id) {
  return freezeMessage({
    id,
    role: 'user',
    content: [{ type: 'text', text: task }],
    source: { kind: 'user' },
  })
}

function existingOutcome(events, id) {
  const prompt = events.find(event => event.type === 'user/message' && event.data.id === id)
  if (prompt === undefined) return undefined
  return summarize(events, prompt.seq)
}

async function openAgent(ctx, sessionId, resume, selection) {
  const options = {
    agentOptions: { provider: selection.provider, model: selection.model },
    setup: (agentCtx) => {
      installModelSelection(agentCtx, { current: selection, assembled: undefined })
    },
  }
  const agents = ctx.get('agents')
  const persistence = ctx.get('sessionPersistence')
  if (persistence === undefined) {
    throw new Error('agentteams-headless-runner: sessionPersistence is required')
  }
  const persisted = (await persistence.list()).some(header => header.id === sessionId)
  if (persisted) {
    return agents.resume({ ...options, resumeSessionId: sessionId })
  }
  if (resume) {
    ctx.logger.warn(`requested resume for missing DSH session ${sessionId}; creating it instead`)
  }
  return agents.create({ ...options, sessionId, meta: { cwd: process.cwd() } })
}

async function run(ctx, config, io) {
  await ctx.get('loader')?.await()
  const defaultModel = ctx.get('agentDefaultModel')
  const sessions = ctx.get('sessions')
  const agents = ctx.get('agents')
  if (defaultModel === undefined || sessions === undefined || agents === undefined) return

  const selection = defaultModel.currentSelection()
  const sessionId = SessionId(config.sessionId || `session-${randomUUID()}`)
  const { agent } = await openAgent(ctx, sessionId, config.resume, selection)
  await agent.whenIdle()

  const id = messageId(config.eventId)
  let outcome = existingOutcome(agent.session.events, id)
  if (outcome === undefined) {
    const firstSeq = agent.session.seq
    agent.followup(userMessage(config.task, id))
    await agent.whenIdle()
    await sessions.flush(agent.session)
    outcome = summarize(agent.session.events, firstSeq)
  } else if (outcome.reason === undefined) {
    throw new Error(`Matrix event ${config.eventId} exists in the DSH session without a completed turn`)
  }

  io.stdout.write((outcome?.text ?? '') + '\n')
  if (outcome?.reason?.kind === 'error') {
    io.stderr.write(`dsh: ${outcome.reason.error.code}: ${outcome.reason.error.message}\n`)
  }
  io.exit(outcome?.reason?.kind === 'completed' ? 0 : 1)
}

export function apply(ctx, config) {
  const exit = ctx.get('appExit')
  if (exit === undefined) {
    throw new Error('agentteams-headless-runner: ctx.appExit is required')
  }
  const io = { stdout: process.stdout, stderr: process.stderr, exit }
  void run(ctx, config, io).catch((error) => {
    io.stderr.write(`dsh: ${error instanceof Error ? error.message : String(error)}\n`)
    io.exit(1)
  })
}
