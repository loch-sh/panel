import { parseISO } from "date-fns";
import i18n from "../i18n";

const currentLocale = () => i18n.resolvedLanguage ?? i18n.language;

export const formatApiDateToString = (date: string) => {
  return parseISO(date).toLocaleDateString(currentLocale());
};

export const formatApiDateToTimeString = (date: string) => {
  return parseISO(date).toLocaleTimeString(currentLocale());
};
