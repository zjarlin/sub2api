<template>
  <section class="edge-context" data-testid="edge-mail-context">
    <header class="edge-section-heading">
      <div>
        <h3>{{ t('admin.vision.mailContext.title') }}</h3>
        <p>{{ t('admin.vision.mailContext.description') }}</p>
      </div>
      <RouterLink to="/admin/accounts?platform=mail" class="edge-button primary">
        <Icon name="plus" size="sm" />{{ t('admin.vision.mailContext.addAccount') }}
      </RouterLink>
    </header>
    <div class="edge-context-summary">
      <span class="edge-state ready"><span />{{ t('admin.vision.mailContext.gatewayManaged') }}</span>
      <span>{{ t('admin.vision.mailContext.proxy') }}</span>
    </div>
    <div class="edge-empty-context">
      <Icon name="mail" size="lg" />
      <h4>{{ t('admin.vision.mailContext.credentials') }}</h4>
      <p>{{ t('admin.vision.mailContext.hint') }}</p>
      <RouterLink to="/admin/accounts?platform=mail" class="edge-button">
        {{ t('admin.vision.mailContext.manageAccounts') }}<Icon name="chevronRight" size="xs" />
      </RouterLink>
    </div>
  </section>
</template>

<script setup lang="ts">
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
</script>
