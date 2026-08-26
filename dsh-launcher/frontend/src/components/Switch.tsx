interface Props {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  title?: string;
}

// Accessible sliding switch (hidden native checkbox + styled track/knob).
export default function Switch({ checked, onChange, disabled, title }: Props) {
  return (
    <label className={`switch ${disabled ? 'disabled' : ''}`} title={title}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
      <span className="switch-track" />
    </label>
  );
}
