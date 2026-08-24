import { readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { parse } from 'yaml'

export const name = 'teamharness-context'
export const inject = ['systemPrompt']

function requiredString(value, field) {
  const text = String(value ?? '').trim()
  if (text === '') throw new Error(`TeamHarness runtime config requires ${field}`)
  return text
}

function rolePromptName(role) {
  const normalized = role.toLowerCase().replaceAll('_', '-').trim()
  if (['team-leader', 'teamleader', 'leader'].includes(normalized)) return 'leader.md'
  if (normalized === 'worker') return 'worker.md'
  if (['remote', 'remote-member'].includes(normalized)) return 'remote-member.md'
  throw new Error(`unsupported TeamHarness role for DSH prototype: ${role}`)
}

export function apply(ctx, config) {
  const pluginDir = resolve(requiredString(config.pluginDir, 'pluginDir'))
  const runtimeConfigPath = resolve(requiredString(config.runtimeConfigPath, 'runtimeConfigPath'))
  const runtimeConfig = parse(readFileSync(runtimeConfigPath, 'utf8'))
  const team = runtimeConfig?.team ?? {}
  const member = runtimeConfig?.member ?? {}
  const role = requiredString(member.role, 'member.role')

  const teamPrompt = readFileSync(join(pluginDir, 'prompts', 'team', 'TEAMS.md'), 'utf8').trim()
  const rolePrompt = readFileSync(
    join(pluginDir, 'prompts', 'agent', rolePromptName(role)),
    'utf8',
  ).trim()
  const currentContext = [
    '# Current Team Context',
    '',
    `team.name: ${requiredString(team.name, 'team.name')}`,
    `team.teamRoomId: ${requiredString(team.teamRoomId, 'team.teamRoomId')}`,
    `team.leaderRuntimeName: ${requiredString(team.leaderRuntimeName, 'team.leaderRuntimeName')}`,
    `member.name: ${requiredString(member.name, 'member.name')}`,
    `member.runtimeName: ${requiredString(member.runtimeName, 'member.runtimeName')}`,
    `member.role: ${role}`,
    `member.matrixUserId: ${requiredString(member.matrixUserId, 'member.matrixUserId')}`,
  ].join('\n')

  const text = `${teamPrompt}\n\n${rolePrompt}\n\n${currentContext}`
  ctx.effect(() => ctx.systemPrompt.section({
    name: 'teamharness:collaboration',
    order: 20,
    text,
  }), 'teamharness-context.section()')
}
