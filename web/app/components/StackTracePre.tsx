// Go stack traces: wrap long paths instead of horizontal scroll.
export default function StackTracePre({
  text,
  className = '',
}: {
  text: string;
  className?: string;
}) {
  return (
    <pre
      className={`whitespace-pre-wrap break-all overflow-x-hidden text-[10px] ${className}`}
    >
      {text}
    </pre>
  );
}
