<script setup lang="ts">
import { computed, onMounted, reactive, ref, useId } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { acme, type ACMEAccount, type ACMEAccountList } from '../acme-api'
import { errorText } from '../format'
import AppIcon from '../components/AppIcon.vue'
import ListState from '../components/ListState.vue'
import PageHeader from '../components/PageHeader.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'

const data = ref<ACMEAccountList | null>(null)
const loading = ref(true)
const loadError = ref('')
const deleteError = ref('')
const deleting = ref(false)
const editing = ref<ACMEAccount | null>(null)
const open = ref(false)
const saving = ref(false)
const saveError = ref('')
const formRef = ref<FormInstance>()
const formId = useId()
const emailHintId = useId()
const eabFieldsId = useId()
const eabHintId = useId()
const manualZeroSSL = ref(false)
const form = reactive({ name: '', provider: 'letsencrypt', email: '', agree_tos: false, eab_kid: '', eab_hmac_key: '' })
const provider = computed(() => data.value?.providers.find(item => item.id === form.provider))
const needsEab = computed(() => editing.value ? Boolean(editing.value.needs_eab) : Boolean(provider.value?.requires_eab))
const zeroSSL = computed(() => form.provider === 'zerossl')
const automaticZeroSSL = computed(() => needsEab.value && zeroSSL.value && !manualZeroSSL.value)
const showCredentials = computed(() => needsEab.value && !automaticZeroSSL.value)
const rules = computed<FormRules>(() => ({
  name: [{ required: true, whitespace: true, message: '请填写账户名称', trigger: 'blur' }],
  provider: [{ required: true, message: '请选择证书提供商', trigger: 'change' }],
  email: [
    { required: automaticZeroSSL.value, message: '请填写联系邮箱', trigger: ['blur', 'change'] },
    { type: 'email', message: '请输入有效的联系邮箱', trigger: 'blur' },
  ],
  agree_tos: [{ validator: (_rule, value) => value === true, message: '请先阅读并同意证书服务条款', trigger: 'change' }],
  eab_kid: [{ required: true, whitespace: true, message: '请填写密钥编号', trigger: ['blur', 'change'] }],
  eab_hmac_key: [{ required: true, whitespace: true, message: '请填写绑定密钥', trigger: ['blur', 'change'] }],
}))

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    data.value = await acme.list()
  } catch (e) {
    loadError.value = errorText(e, '读取证书账户失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function edit(account?: ACMEAccount) {
  editing.value = account ?? null
  saveError.value = ''
  manualZeroSSL.value = false
  Object.assign(form, {
    name: account?.name ?? '',
    provider: account?.provider ?? 'letsencrypt',
    email: account?.email ?? '',
    agree_tos: account?.agree_tos ?? false,
    eab_kid: '',
    eab_hmac_key: '',
  })
  formRef.value?.clearValidate()
  open.value = true
}

function changeProvider() {
  Object.assign(form, { agree_tos: false, eab_kid: '', eab_hmac_key: '' })
  manualZeroSSL.value = false
  formRef.value?.clearValidate(['provider', 'email', 'agree_tos', 'eab_kid', 'eab_hmac_key'])
}

async function submit() {
  if (saving.value || !await formRef.value?.validate().catch(() => false)) return
  saving.value = true
  saveError.value = ''
  try {
    const saved = await acme.save(editing.value?.id ?? '', {
      name: form.name.trim(),
      provider: form.provider,
      email: form.email.trim(),
      agree_tos: form.agree_tos,
      ...(showCredentials.value ? { eab_kid: form.eab_kid.trim(), eab_hmac_key: form.eab_hmac_key.trim() } : {}),
    })
    if (saved.needs_eab) {
      saveError.value = automaticZeroSSL.value
        ? '账户已保存，但 ZeroSSL 注册未完成。请核对邮箱后重试，或手动填写绑定凭据。'
        : `账户已保存，但 ${saved.provider_name} 注册未完成。请核对绑定凭据和网络连接后重试。`
      editing.value = saved
    } else {
      ElMessage.success(editing.value ? '账户已保存' : '账户已添加')
      open.value = false
    }
    await load()
  } catch (e) {
    saveError.value = errorText(e, '保存证书账户失败')
  } finally {
    saving.value = false
  }
}

async function remove(account: ACMEAccount) {
  try {
    await ElMessageBox.confirm(`删除证书账户「${account.name}」？`, '删除账户', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger',
    })
  } catch { return }
  deleting.value = true
  deleteError.value = ''
  try {
    await acme.remove(account.id)
    ElMessage.success('账户已删除')
    await load()
  } catch (e) {
    deleteError.value = errorText(e, '删除证书账户失败')
  } finally {
    deleting.value = false
  }
}

const menu = (account: ACMEAccount): MenuItem[] => [
  { label: '编辑', icon: 'edit', onSelect: () => edit(account) },
  { label: '删除', icon: 'trash', danger: true, divided: true, onSelect: () => remove(account) },
]
</script>

<template>
  <PageHeader title="ACME 账户管理" description="添加证书提供商账户，申请 HTTPS 证书时可选择使用哪一个。">
    <RouterLink to="/domains" class="link">返回域名与证书</RouterLink>
    <el-button type="primary" :disabled="loading || !!loadError || deleting" @click="edit()"><AppIcon name="plus"/>添加账户</el-button>
  </PageHeader>
  <el-alert v-if="deleteError" :title="deleteError" type="error" show-icon @close="deleteError = ''"/>
  <div class="panel">
    <ListState :loading="loading" :error="loadError" :empty="!data?.accounts.length" icon="cert" title="还没有证书账户" description="添加第一个账户后，就可以申请 HTTPS 证书。" @retry="load">
      <div class="tbl-wrap">
        <table class="rtable" aria-label="证书账户">
          <thead><tr><th scope="col">账户</th><th scope="col">证书提供商</th><th scope="col">联系邮箱</th><th scope="col"><span class="sr">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="account in data?.accounts" :key="account.id">
              <td class="first">
                <span class="name">{{ account.name }}</span>
                <span v-if="account.id === data?.default_account_id" class="sub"><span class="tag">默认账户</span></span>
                <span v-if="account.needs_eab || !account.agree_tos" class="sub">待完成配置</span>
              </td>
              <td data-label="证书提供商">{{ account.provider_name }}</td>
              <td data-label="联系邮箱">{{ account.email || '未填写' }}</td>
              <td class="actions"><span class="row-act"><RowMenu :items="menu(account)" :label="account.name" :disabled="deleting"/></span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </ListState>
  </div>
  <el-dialog v-model="open" :title="editing ? '编辑证书账户' : '添加证书账户'" width="min(520px, calc(100vw - 32px))" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" append-to-body @closed="form.eab_kid = ''; form.eab_hmac_key = ''">
    <el-form :id="formId" ref="formRef" :model="form" :rules="rules" :disabled="saving" :validate-on-rule-change="false" label-position="top" class="dialog-form" scroll-to-error novalidate @submit.prevent="submit">
      <el-form-item label="账户名称" prop="name">
        <el-input v-model="form.name" name="name" placeholder="例如 公司证书账户" required/>
      </el-form-item>
      <el-form-item v-if="editing" label="证书提供商">{{ editing.provider_name }}</el-form-item>
      <el-form-item v-else label="证书提供商" prop="provider">
        <el-select v-model="form.provider" name="provider" @change="changeProvider">
          <el-option v-for="option in data?.providers" :key="option.id" :value="option.id" :label="option.name"/>
        </el-select>
      </el-form-item>
      <el-form-item :label="automaticZeroSSL ? '联系邮箱' : '联系邮箱（可选）'" prop="email">
        <el-input v-model.trim="form.email" name="email" type="email" autocomplete="email" autocapitalize="none" spellcheck="false" :required="automaticZeroSSL" :aria-describedby="automaticZeroSSL ? emailHintId : undefined"/>
        <span v-if="automaticZeroSSL" :id="emailHintId" class="hint">面板会将此邮箱发送给 ZeroSSL 以完成账户注册。</span>
      </el-form-item>
      <p v-if="needsEab && zeroSSL" class="hint">
        <el-button link type="primary" native-type="button" :aria-expanded="manualZeroSSL" :aria-controls="eabFieldsId" @click="manualZeroSSL = !manualZeroSSL; formRef?.clearValidate('email')">
          {{ manualZeroSSL ? '改用邮箱自动注册' : '手动填写绑定凭据' }}
        </el-button>
      </p>
      <div v-if="needsEab" :id="eabFieldsId" :key="form.provider">
        <template v-if="showCredentials">
          <p v-if="zeroSSL" :id="eabHintId" class="hint">请从 <a class="link" href="https://app.zerossl.com/developer" target="_blank" rel="noopener">ZeroSSL 控制台</a> 的开发者设置获取账户绑定凭据。</p>
          <p v-else :id="eabHintId" class="hint">请在你的 Google Cloud 项目中按<a class="link" href="https://docs.cloud.google.com/certificate-manager/docs/public-ca-tutorial" target="_blank" rel="noopener">官方指南</a>生成账户绑定凭据。</p>
          <el-form-item label="密钥编号（EAB Key ID）" prop="eab_kid">
            <el-input v-model="form.eab_kid" name="eab_kid" :aria-describedby="eabHintId" autocapitalize="none" spellcheck="false" required/>
          </el-form-item>
          <el-form-item label="绑定密钥（EAB HMAC Key）" prop="eab_hmac_key">
            <el-input v-model="form.eab_hmac_key" name="eab_hmac_key" :aria-describedby="eabHintId" type="password" autocomplete="new-password" required/>
          </el-form-item>
        </template>
      </div>
      <el-form-item prop="agree_tos">
        <el-checkbox v-model="form.agree_tos" name="agree_tos" :validate-event="false" @change="formRef?.clearValidate('agree_tos')">我同意证书服务条款</el-checkbox>
        <a v-if="provider" :href="provider.tos_url" target="_blank" rel="noopener" class="link">查看 {{ provider.name }} 服务条款</a>
      </el-form-item>
      <el-alert v-if="saveError" :title="saveError" type="error" :closable="false" show-icon/>
    </el-form>
    <template #footer>
      <el-button :disabled="saving" @click="open = false">取消</el-button>
      <el-button type="primary" native-type="submit" :form="formId" :loading="saving">保存账户</el-button>
    </template>
  </el-dialog>
</template>
