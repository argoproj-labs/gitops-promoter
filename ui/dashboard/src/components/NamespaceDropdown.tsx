import React, { useEffect } from 'react';
import Select, { components, OptionProps, SingleValue } from 'react-select';
import { FaFolder } from 'react-icons/fa';
import { namespaceStore } from '../stores/NamespaceStore';
import './NamespaceDropdown.scss';

interface NamespaceStore {
  namespace: string;
  namespaces: string[];
  setNamespace: (namespace: string) => void;
  setNamespaces: (namespaces: string[]) => void;
}

interface SelectOption {
  value: string;
  label: string;
}

const NamespaceDropdown: React.FC = () => {
  const namespaces = namespaceStore((s: NamespaceStore) => s.namespaces);
  const setNamespace = namespaceStore((s: NamespaceStore) => s.setNamespace);
  const setNamespaces = namespaceStore((s: NamespaceStore) => s.setNamespaces);
  const namespace = namespaceStore((s: NamespaceStore) => s.namespace);

  // Fetching namespace from API
  useEffect(() => {
    fetch('/list?kind=namespace')
      .then(res => res.json())
      .then(data => setNamespaces(Array.isArray(data) ? data : []))
      .catch(() => setNamespace('default'));
  }, [setNamespaces]);

  const options = Array.isArray(namespaces) ? namespaces.map((ns: string) => ({
    value: ns,
    label: ns
  })) : [];

  //Rendering
  const Option = (props: OptionProps<SelectOption>) => (
    <components.Option {...props}>
      <FaFolder style={{ marginRight: 8, color: '#7b8a97' }} />
      {props.data.label}
    </components.Option>
  );

  return (
    <Select<SelectOption>
      classNamePrefix="namespace-dropdown"
      options={options}
      placeholder="Select a namespace"
      value={options.find((opt: SelectOption) => opt.value === namespace) || null}
      onChange={(option: SingleValue<SelectOption>) => setNamespace(option ? option.value : '')}
      components={{ Option }}
      isClearable
    />
  );
};

export { NamespaceDropdown }; 