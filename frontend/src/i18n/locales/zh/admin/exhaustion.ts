export default {
  exhaustion: {
    title: '榨干模式',
    helpLabel: '榨干模式说明',
    help: 'Auto 按列表顺序优先使用此账号的模型；指定模型请求只优先对应模型。共用账号并发上限，满并发或不可用时进入其他账号池。权限和原生会话续接约束始终生效。',
    invalid: '请选择 1–16 个已配置的文本模型；清单中的模型和分组必须仍属于此账号。',
    selectModel: '选择优先使用的模型',
    add: '添加',
    up: '上移 {model}',
    down: '下移 {model}',
    remove: '移除 {model}',
    groups: '生效分组',
    allGroups: '所有已绑定分组'
  }
}
