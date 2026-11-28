// Capture the native Apps Script configuration controls the connector builds.
export class Config {
  questions = [];
  newSelectSingle() { return this.entry('select'); }
  newTextArea() { return this.entry('textarea'); }
  newInfo() {
    const q = {type: 'info'};
    this.questions.push(q);
    return {
      setId(id) { q.id = id; return this; },
      setText(text) { q.text = text; return this; }
    };
  }
  entry(type) {
    const q = {type, choices: [], labels: []};
    this.questions.push(q);
    return {
      setId(id) { q.id = id; return this; },
      setName(name) { q.name = name; return this; },
      setHelpText(help) { q.help = help; return this; },
      setPlaceholder(example) { q.example = example; return this; },
      setAllowOverride(value) { q.override = value; return this; },
      setIsDynamic(value) { q.dynamic = value; return this; },
      addOption(option) { q.choices.push(option.value); q.labels.push(option.label); return this; }
    };
  }
  newOptionBuilder() { return {setLabel(label) { this.label = label; return this; }, setValue(value) { this.value = value; return this; }}; }
  setIsSteppedConfig(value) { this.stepped = value; }
  setDateRangeRequired(value) { this.dateRequired = value; }
  build() { return this; }
}
