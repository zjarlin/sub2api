import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import plugins from './plugins'
import edgeWeather from './edgeWeather'

export default {
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...plugins,
  vision: {
    ...settings.vision,
    ...edgeWeather.vision,
    catalog: {
      ...settings.vision.catalog,
      ...edgeWeather.vision.catalog,
      fields: { ...settings.vision.catalog.fields, ...edgeWeather.vision.catalog.fields },
    },
  },
}
