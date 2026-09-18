function parseDate(value?: string): Date | null {
  if (!value) return null;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? null : date;
}

function isSameLocalDay(left: Date, right: Date): boolean {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  );
}

function localDayNumber(value: Date): number {
  return Date.UTC(
    value.getFullYear(),
    value.getMonth(),
    value.getDate(),
  ) / 86_400_000;
}

export interface RelativeDayLabels {
  yesterday: string;
  dayBeforeYesterday: string;
}

export function formatSessionTime(
  value: string,
  locale: string,
  now = new Date(),
  labels: RelativeDayLabels = {
    yesterday: "Yesterday",
    dayBeforeYesterday: "Day before yesterday",
  },
): string {
  const date = parseDate(value);
  if (!date) return "";
  if (isSameLocalDay(date, now)) {
    return new Intl.DateTimeFormat(locale, {
      hour: "numeric",
      minute: "2-digit",
    }).format(date);
  }
  const daysAgo = localDayNumber(now) - localDayNumber(date);
  if (daysAgo === 1) return labels.yesterday;
  if (daysAgo === 2) return labels.dayBeforeYesterday;
  return new Intl.DateTimeFormat(locale, {
    ...(date.getFullYear() === now.getFullYear()
      ? {}
      : { year: "2-digit" as const }),
    month: "numeric",
    day: "numeric",
  }).format(date);
}

export function formatMessageTime(
  value: string | undefined,
  locale: string,
  now = new Date(),
): string {
  const date = parseDate(value);
  if (!date) return "";
  return new Intl.DateTimeFormat(locale, {
    ...(isSameLocalDay(date, now)
      ? {}
      : {
          year:
            date.getFullYear() === now.getFullYear()
              ? undefined
              : ("numeric" as const),
          month: "short" as const,
          day: "numeric" as const,
        }),
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
}

export function formatFullTime(
  value: string | undefined,
  locale: string,
): string {
  const date = parseDate(value);
  if (!date) return "";
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}
