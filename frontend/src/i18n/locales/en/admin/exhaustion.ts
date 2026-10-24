export default {
  exhaustion: {
    title: 'Exhaustion mode',
    helpLabel: 'About exhaustion mode',
    help: 'Auto prefers this account’s models in the listed order. Explicit model requests only prefer the matching model. All models share the account concurrency limit. Other accounts take over when full or unavailable. Permissions and native continuation bindings still apply.',
    invalid: 'Select 1–16 configured text models. Listed models and groups must still belong to this account.',
    selectModel: 'Select a preferred model',
    add: 'Add',
    up: 'Move {model} up',
    down: 'Move {model} down',
    remove: 'Remove {model}',
    groups: 'Apply to groups',
    allGroups: 'All assigned groups'
  }
}
