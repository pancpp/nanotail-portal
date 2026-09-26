import { useI18n } from '../i18n'
import LANSettingsForm from '../components/LANSettingsForm'
import TailnetConnectionForm from '../components/TailnetConnectionForm'

export default function NetworkPage() {
  const { t } = useI18n()
  return <>
    <section className="page-heading"><div><h1>{t("Network")}</h1><p>{t("Manage this device’s tailnet connection and LAN address.")}</p></div></section>
    <TailnetConnectionForm />
    <LANSettingsForm />
  </>
}
