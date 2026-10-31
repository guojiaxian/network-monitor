<template>
  <div class="container" style="max-width: 400px; padding-top: 14vh">
    <div class="nw-card">
      <h4 class="mb-3 text-center">🛰️ NetWatch</h4>
      <input v-model="username" class="form-control mb-2" placeholder="用户名" @keyup.enter="login" />
      <input v-model="password" type="password" class="form-control mb-3" placeholder="密码" @keyup.enter="login" />
      <button class="btn btn-primary w-100" @click="login">登 录</button>
      <div v-if="msg" class="text-danger small mt-2">{{ msg }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'

const router = useRouter()
const username = ref(''), password = ref(''), msg = ref('')

async function login() {
  msg.value = ''
  try {
    await api.post('/api/admin/login', { username: username.value, password: password.value })
    router.push('/')
  } catch (e) { msg.value = e.message }
}
</script>
