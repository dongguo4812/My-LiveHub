import { ref } from 'vue'
import * as AppearanceService from '../../bindings/github.com/dongguo4812/My-LiveHub/app/internal/desktop/appearanceservice.js'
export type Appearance = Awaited<ReturnType<typeof AppearanceService.GetAppearance>>
export const appearance=ref<Appearance>({theme:'dark',accent_color:'indigo'})
export const appearanceError=ref('')
export const appearanceBusy=ref(false)
export const logRetentionDays=ref(14)
export const logRetentionBusy=ref(false)
export const logRetentionError=ref('')
export function applyAppearance(value:Appearance){appearance.value=value;document.documentElement.dataset.theme=value.theme;document.documentElement.dataset.accent=value.accent_color;document.documentElement.style.colorScheme=value.theme}
export async function loadAppearance(){try{applyAppearance(await AppearanceService.GetAppearance());logRetentionDays.value=await AppearanceService.GetLogRetentionDays();appearanceError.value='';logRetentionError.value=''}catch(error){appearanceError.value=error instanceof Error?error.message:String(error);applyAppearance({theme:'dark',accent_color:'indigo'})}}
export async function saveAppearance(theme:string,accentColor:string){appearanceBusy.value=true;try{applyAppearance(await AppearanceService.UpdateAppearance(theme,accentColor));appearanceError.value='';return true}catch(error){appearanceError.value=error instanceof Error?error.message:String(error);return false}finally{appearanceBusy.value=false}}
export async function saveLogRetentionDays(days:number){logRetentionBusy.value=true;try{logRetentionDays.value=await AppearanceService.UpdateLogRetentionDays(days);logRetentionError.value='';return true}catch(error){logRetentionError.value=error instanceof Error?error.message:String(error);return false}finally{logRetentionBusy.value=false}}
