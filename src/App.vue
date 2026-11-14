<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { TosApiError } from 'tos-app-core'
import { RefreshCw, Send } from '@lucide/vue'
import { useTosApp } from 'tos-app-core/vue'
import { TosAppShell, TosConnectionReport, TosPreferencesForm } from 'tos-app-ui-kit'
import { metadata } from './app.ts'

const { core, language, accessFailure } = useTosApp()
const text = (zh: string, en: string) => language.value === 'en-US' ? en : zh
const info = ref<{ id: string; version: string; mode: string }>()
const message = ref('')
const reply = ref('')
const loading = ref(false)
const sending = ref(false)
const error = ref<TosApiError | null>(null)
const controller = new AbortController()
onUnmounted(() => controller.abort())

const errorText = computed(() => {
  if (!error.value) return ''
  if (error.value.kind === 'session') return text('登录已失效，正在前往 TOS 登录页。', 'Your session expired. Opening the TOS login page.')
  if (error.value.kind === 'origin') return text('连接来源不匹配，请从 TOS 桌面重新打开应用。', 'The request origin does not match. Reopen the app from TOS.')
  if (error.value.kind === 'denied') return text('TOS 拒绝访问，请检查当前账号权限。', 'TOS denied access. Check your account permissions.')
  if (error.value.code === 'MESSAGE_INVALID') return text('请输入 1 至 200 个字符。', 'Enter 1 to 200 characters.')
  return text('连接失败，请查看诊断并重试。', 'Connection failed. Check the diagnostic report and retry.')
})
function rememberError(value: unknown) {
  if (!controller.signal.aborted) error.value = value instanceof TosApiError ? value : new TosApiError('invalid-response')
}
async function connect() {
  loading.value = true
  error.value = null
  info.value = undefined
  reply.value = ''
  core.api.reset()
  try { info.value = await core.api.request('/info', { signal: controller.signal }) }
  catch (value) { rememberError(value) }
  finally { loading.value = false }
}
async function send() {
  sending.value = true
  error.value = null
  reply.value = ''
  try {
    const result = await core.api.request<{ message: string }>('/echo', { method: 'POST', body: { message: message.value }, signal: controller.signal })
    reply.value = result.message
  } catch (value) { rememberError(value) }
  finally { sending.value = false }
}
onMounted(connect)
</script>

<template>
  <TosAppShell :title="metadata.name[language]">
    <div class="app-content">
    <p v-if="accessFailure" class="error" role="alert">{{ errorText }}</p>
    <template v-else>
    <section aria-labelledby="connection-title">
      <div class="section-heading">
        <h2 id="connection-title">{{ text('服务连接', 'Service connection') }}</h2>
        <button class="button secondary" :disabled="loading || sending" @click="connect"><RefreshCw :size="16" :stroke-width="1.6" aria-hidden="true" />{{ loading ? text('连接中…', 'Connecting…') : text('重新连接', 'Reconnect') }}</button>
      </div>
      <p role="status" aria-live="polite">{{ loading ? text('正在读取服务状态。', 'Reading service status.') : info ? text('服务已连接。', 'Service connected.') : text('尚未连接。', 'Disconnected.') }}</p>
      <dl v-if="info">
        <dt>{{ text('前端 / 后端版本', 'Frontend / backend version') }}</dt><dd>{{ metadata.version }} / {{ info.version }}</dd>
        <dt>{{ text('运行环境', 'Environment') }}</dt><dd>{{ info.mode === 'development' ? text('本地开发', 'Local development') : 'TOS' }}</dd>
      </dl>
    </section>

    <section aria-labelledby="example-title">
      <h2 id="example-title">{{ text('请求示例', 'Request example') }}</h2>
      <p class="muted">{{ text('发送一段文字，服务会原样返回。示例不会保存内容。', 'Send text and the service returns it unchanged. This example does not save content.') }}</p>
      <form @submit.prevent="send">
        <label class="field-label" for="message">{{ text('文字', 'Text') }}
          <input id="message" v-model="message" required maxlength="200" :disabled="sending" autocomplete="off" />
        </label>
        <button class="button primary" type="submit" :disabled="!info || !!accessFailure || sending || loading || !message.trim()"><Send :size="16" :stroke-width="1.6" aria-hidden="true" />{{ sending ? text('发送中…', 'Sending…') : text('发送文字', 'Send text') }}</button>
      </form>
      <p v-if="reply" class="reply" role="status">{{ text('服务返回：', 'Service returned: ') }}{{ reply }}</p>
    </section>

    <p v-if="error" class="error" role="alert">{{ errorText }}</p>
    <TosConnectionReport />

    <section aria-labelledby="settings-title">
      <h2 id="settings-title">{{ text('显示设置', 'Display settings') }}</h2>
      <TosPreferencesForm />
    </section>
    </template>
    </div>
  </TosAppShell>
</template>
