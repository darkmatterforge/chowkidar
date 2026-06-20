export default function RestartIcon(props: { size?: number; class?: string }) {
  const size = props.size ?? 16
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 50 50"
      fill="currentColor"
      class={props.class}
      aria-hidden="true"
    >
      <path d="M 25 2 A 1.0001 1.0001 0 1 0 25 4 C 36.609534 4 46 13.390466 46 25 C 46 36.609534 36.609534 46 25 46 C 13.390466 46 4 36.609534 4 25 C 4 18.776502 6.7056023 13.200205 11 9.3554688 L 11 17 A 1.0001 1.0001 0 1 0 13 17 L 13 7 A 1.0001 1.0001 0 0 0 12 6 L 2 6 A 1.0001 1.0001 0 1 0 2 8 L 9.5234375 8 C 4.9051803 12.207192 2 18.26679 2 25 C 2 37.690466 12.309534 48 25 48 C 37.690466 48 48 37.690466 48 25 C 48 12.309534 37.690466 2 25 2 z" />
    </svg>
  )
}
