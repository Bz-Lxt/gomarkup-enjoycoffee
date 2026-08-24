type ClassValue = string | number | false | null | undefined | ClassValue[];

/** 极简 classnames。不引依赖，因为需要的就是这七行。 */
export function cx(...parts: ClassValue[]): string {
  const out: string[] = [];
  for (const p of parts) {
    if (!p) continue;
    if (Array.isArray(p)) {
      const nested = cx(...p);
      if (nested) out.push(nested);
    } else {
      out.push(String(p));
    }
  }
  return out.join(' ');
}
