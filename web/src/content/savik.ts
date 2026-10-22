// Сейвик в приложении: какая поза у какого состояния и что он говорит.
// Правьте свободно — это контент, а не логика. Фраза выбирается случайно из
// списка своего состояния. Позы — файлы web/public/savik/{512,256}/<имя>.webp,
// их собирает `npm run poses` из poses/*.png.

/** Every pose that exists in web/public/savik. */
export type Pose =
  | "01-neutral"
  | "02-talk"
  | "03-joy"
  | "04-shock"
  | "05-sad"
  | "06-cry"
  | "07-angry"
  | "08-sly"
  | "09-proud"
  | "10-point-left"
  | "11-point-right"
  | "12-think"
  | "13-phone"
  | "14-fight"
  | "15-damage"
  | "16-hide-coin"
  | "17-sleep"
  | "18-hungry"
  | "19-empty"
  | "20-full"
  | "21-debt";

/**
 * Moods of Savik. "neutral" and "empty" come from the API (GET /savik, the
 * money of the month); the rest are moments of the app itself.
 */
export type Mood =
  | "neutral" // nothing alarming
  | "empty" // this month's expenses are above the incomes
  | "saved" // an operation was just recorded
  | "welcome" // first launch, before the data policy is accepted
  | "outside" // opened outside Telegram
  | "error"; // something failed

export const poses: Record<Mood, Pose> = {
  neutral: "01-neutral",
  empty: "19-empty",
  saved: "03-joy",
  welcome: "02-talk",
  outside: "12-think",
  error: "05-sad",
};

export const phrases: Record<Mood, string[]> = {
  neutral: [
    "Записывай траты — я всё посчитаю.",
    "Каждая записанная трата — плюс один к опыту.",
    "Сундук на месте. Что сегодня?",
  ],
  empty: [
    "В этом месяце траты обогнали доходы. Затянем крышку?",
    "Монет больше уходит, чем приходит. Посмотрим, куда?",
  ],
  saved: ["Записано! +1 XP.", "Готово, сундук в курсе.", "Есть! Так держать."],
  welcome: ["Привет! Я Сейвик — сундук, который учится хранить, а не тратить."],
  outside: ["Хм, кто здесь? Открой меня из Telegram."],
  error: ["Что-то пошло не так. Попробуем ещё раз?"],
};

/** A random phrase of a mood. */
export function phraseOf(mood: Mood): string {
  const list = phrases[mood];
  return list[Math.floor(Math.random() * list.length)];
}
