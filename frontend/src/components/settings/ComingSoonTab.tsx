interface Props {
  name: string;
}

export function ComingSoonTab({ name }: Props) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <h3 className="text-lg font-semibold">{name}</h3>
      <p className="mt-2 text-sm text-muted-foreground">Coming soon</p>
    </div>
  );
}
