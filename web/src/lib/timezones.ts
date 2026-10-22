// Russian timezones, west to east: the same list as /settings in the bot.
const zones: [city: string, tz: string][] = [
  ["Калининград", "Europe/Kaliningrad"],
  ["Москва", "Europe/Moscow"],
  ["Самара", "Europe/Samara"],
  ["Екатеринбург", "Asia/Yekaterinburg"],
  ["Омск", "Asia/Omsk"],
  ["Новосибирск", "Asia/Novosibirsk"],
  ["Красноярск", "Asia/Krasnoyarsk"],
  ["Иркутск", "Asia/Irkutsk"],
  ["Якутск", "Asia/Yakutsk"],
  ["Владивосток", "Asia/Vladivostok"],
  ["Магадан", "Asia/Magadan"],
  ["Камчатка", "Asia/Kamchatka"],
];

function offset(tz: string): string {
  const part = new Intl.DateTimeFormat("en-US", { timeZone: tz, timeZoneName: "shortOffset" })
    .formatToParts(new Date())
    .find((p) => p.type === "timeZoneName");
  return part?.value.replace("GMT", "UTC") ?? "";
}

/** Options for a timezone picker; the current zone is kept even if it is not Russian. */
export function timezoneOptions(current: string): { value: string; label: string }[] {
  const options = zones.map(([city, tz]) => ({ value: tz, label: `${city}, ${offset(tz)}` }));
  if (!options.some((o) => o.value === current)) {
    options.unshift({ value: current, label: `${current}, ${offset(current)}` });
  }
  return options;
}
