import { ref } from 'vue'
import * as BilibiliAuthService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/bilibiliauthservice.js'

export type BilibiliAccount = Awaited<ReturnType<typeof BilibiliAuthService.GetAccount>>
export type BilibiliLoginSession = Awaited<ReturnType<typeof BilibiliAuthService.StartLogin>>
export type BilibiliLoginStatus = Awaited<ReturnType<typeof BilibiliAuthService.PollLogin>>

export const bilibiliAccount = ref<BilibiliAccount>(null)
export const bilibiliLoginSession = ref<BilibiliLoginSession | null>(null)
export const bilibiliLoginStatus = ref<BilibiliLoginStatus | null>(null)
export const bilibiliAuthError = ref('')
export const bilibiliAuthBusy = ref(false)

export const bilibiliCookieBusy = ref(false)

export async function loginBilibiliWithCookie(cookie: string) {
  bilibiliCookieBusy.value = true
  bilibiliAuthError.value = ''
  try {
    bilibiliAccount.value = await BilibiliAuthService.LoginWithCookie(cookie)
    bilibiliLoginSession.value = null
    bilibiliLoginStatus.value = { status: 'success', message: 'Cookie 登录成功', account: bilibiliAccount.value ?? undefined }
    return true
  } catch (error) {
    bilibiliAuthError.value = error instanceof Error ? error.message : String(error)
    return false
  } finally {
    bilibiliCookieBusy.value = false
  }
}

export async function refreshBilibiliAccount() {
  try {
    bilibiliAccount.value = await BilibiliAuthService.GetAccount()
    bilibiliAuthError.value = ''
  } catch (error) {
    bilibiliAuthError.value = error instanceof Error ? error.message : String(error)
  }
}

export async function startBilibiliLogin() {
  bilibiliAuthBusy.value = true
  bilibiliAuthError.value = ''
  bilibiliLoginStatus.value = null
  try {
    bilibiliLoginSession.value = await BilibiliAuthService.StartLogin()
  } catch (error) {
    bilibiliAuthError.value = error instanceof Error ? error.message : String(error)
  } finally {
    bilibiliAuthBusy.value = false
  }
}

export async function pollBilibiliLogin() {
  if (!bilibiliLoginSession.value) return
  try {
    const status = await BilibiliAuthService.PollLogin(bilibiliLoginSession.value.id)
    bilibiliLoginStatus.value = status
    if (status.status === 'success') {
      bilibiliAccount.value = status.account ?? null
      bilibiliLoginSession.value = null
    } else if (status.status === 'expired') {
      bilibiliLoginSession.value = null
    }
  } catch (error) {
    bilibiliAuthError.value = error instanceof Error ? error.message : String(error)
  }
}

export async function logoutBilibili() {
  bilibiliAuthBusy.value = true
  try {
    await BilibiliAuthService.Logout()
    bilibiliAccount.value = null
    bilibiliLoginSession.value = null
    bilibiliLoginStatus.value = null
    bilibiliAuthError.value = ''
  } catch (error) {
    bilibiliAuthError.value = error instanceof Error ? error.message : String(error)
  } finally {
    bilibiliAuthBusy.value = false
  }
}
