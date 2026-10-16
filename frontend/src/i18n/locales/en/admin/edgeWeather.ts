// Edge weather copy; shallow-merged into the vision node to avoid touching the large settings file.
export default {
  vision: {
    endpointWeather: 'Unified weather lookup (current, forecast and history)',
    endpointWeatherProviders: 'List weather sources with capabilities',
    catalog: {
      weatherTitle: 'Weather',
      weatherDescription: 'Unified weather lookups with automatic failover across free public sources.',
      utility: 'Utility',
      categoryUtility: 'Utility',
      categoryUtilityDescription: 'Foundational data services such as weather.',
      fields: {
        weatherLocation: 'City name, administrative code or "lat,lon". Names use global sources; local codes use domestic ones.',
        weatherDays: 'Forecast days, 1-16, default 3.',
        weatherHistory: 'Set true to return daily history rows for start/end.',
        weatherRange: 'History range start and end as YYYY-MM-DD; defaults to the last complete month.',
        weatherProvider: 'Pin one source (openmeteo, wttr, chinasky, tianqi2345); omit to fail over automatically.',
      },
    },
  },
}
