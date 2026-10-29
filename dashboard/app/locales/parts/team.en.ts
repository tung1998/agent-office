import type vi from './team.vi'

const en: Record<keyof typeof vi, string> = {
  'team.agentCount': '{n} agents',
  'team.noAgents': 'No agents yet',
  'team.default': 'Default',
  'team.defaultHint': 'This agent answers when a chat, a bot message (Discord/Telegram) or an automation does not name a specific agent. Use this when a project has several agents but should still have one that answers in general cases. Making another agent default retires this one from that role; its past chats stay as they are.',
  'team.defaultAgent': 'Default agent',
  'team.defaultAgentLower': 'default agent',
  'team.defaultSet': '{name} is now the default agent',
  'team.makeDefault': 'Make default',
  'team.open': 'Open agent page',
  'team.export': 'Download agents JSON',
  'team.addAndConfigure': 'Add and configure',
  'team.empty.title': 'This project has no agents yet',
  'team.empty.desc': 'Add agents from the library, or let AI pick what the project needs.',

  // AgentLibrary (ADR-153)
  'team.lib.addOne': 'Add one agent from the library',
  'team.lib.all': 'All',
  'team.lib.pageTitle': 'Agent library',
  'team.lib.pageDesc': 'Agent templates to add to a project, or to read as a model when writing your own instructions. Click one to see it in full.',
  'team.lib.pickProject': 'Pick a project',
  'team.lib.toProject': 'Add to project',
  'team.lib.featured': 'Featured',
  'team.lib.cat.design': 'Design',
  'team.lib.count': '{n} agents',
  'team.lib.words': '{n} words',
  'team.lib.agencyNote': 'Original English text, fairly long: trim it after adding to save tokens',
  'team.lib.cat.code': 'Code',
  'team.lib.cat.plan': 'Planning',
  'team.lib.cat.customer': 'Customers',
  'team.lib.cat.content': 'Content',
  'team.lib.search': 'Search agents…',
  'team.lib.none': 'No agent matches',
  'team.lib.source': 'Original',
  'team.lib.use': 'Use this one',
  'team.lib.from': 'Based on {name}: with its description, instructions and permissions',
  'team.lib.skills': 'Comes with skills:',
  'team.lib.requiresPack': 'Requires {pack}',
  'team.lib.packHelp': 'This template uses workflows, skills and data from the project pack above. Adding the agent alone does not install those dependencies. Before use, check that the project has them; otherwise create a project from that pack or adapt the instructions to the current project.',
  'team.lib.skillsHelp': 'The agent keeps these skills at hand. Ones the project lacks are installed from the office library into the project\'s skills folder.',
  'team.lib.back': 'Pick another',
  'team.rev.library': 'added from the library: {arg}',
  'team.rev.setup': 'AI setup: {arg}',


  // RevisionHistory
  'team.rev.intro': 'Each time agents change, office saves the state just before. Restoring is saved too, so it can always be undone.',
  'team.rev.restoreConfirm': 'Bring the project\'s agents back to this state? The current state is still kept in the history.',
  'team.rev.pack': 'applying pack {arg}',

  // setup with AI
  'team.setup.hasAgentsWarn': 'The project has {n} agents, kept as they are. The setup only adds what is missing from the library.',

  // home: getting started
  'team.home.step3Title': 'Give projects their agents',
  'team.home.step3TextDone': '{n}/{total} projects have agents',
  'team.home.step3TextTodo': 'Pick agents from the library, or let AI pick',
}

export default en
