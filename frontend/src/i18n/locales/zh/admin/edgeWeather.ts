// 边缘天气服务的文案；与 settings.ts 的 vision 节点做浅层合并，避免改动大文件。
export default {
  vision: {
    endpointWeather: '统一天气查询（当前、预报与历史）',
    endpointWeatherProviders: '查询可用天气数据源与能力',
    catalog: {
      weatherTitle: '天气服务',
      weatherDescription: '统一天气查询，多个免密钥公开数据源按健康分自动回退。',
      utility: '实用工具',
      categoryUtility: '实用工具',
      categoryUtilityDescription: '天气等基础数据服务。',
      fields: {
        weatherLocation: '城市名、行政区划编码或“纬度,经度”。城市名走全球源，国内编码走本地源。',
        weatherDays: '预报天数，1–16，默认 3。',
        weatherHistory: '设为 true 时按 start/end 返回历史日线数据。',
        weatherRange: '历史区间起止日期，格式 YYYY-MM-DD；省略时取最近一个完整自然月。',
        weatherProvider: '指定单一数据源（openmeteo、wttr、chinasky、tianqi2345），省略时自动回退。',
      },
    },
  },
}
