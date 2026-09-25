import NodeKeyRenewalDialog from './NodeKeyRenewalDialog'

export default function TailscaleSetupPrompt({ onClose }: { onClose: () => void }) {
  return <NodeKeyRenewalDialog signIn onClose={onClose} />
}
