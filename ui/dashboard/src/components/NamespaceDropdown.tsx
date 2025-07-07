import React, { useEffect } from 'react';
import Select, { components } from 'react-select';
import { FaFolder } from 'react-icons/fa';
import { namespaceStore } from '@shared/stores/NamespaceStore';
import './NamespaceDropdown.scss';

const NamespaceDropdown: React.FC = () => {
  const namespaces = namespaceStore((s: any) => s.namespaces);
  const setNamespace = namespaceStore((s: any) => s.setNamespace);
  const setNamespaces = namespaceStore((s: any) => s.setNamespaces);
  const namespace = namespaceStore((s: any) => s.namespace);

  // Fetching namespace from API
  useEffect(() => {
    fetch('/list?kind=namespace')
      .then(res => res.json())
      .then(data => setNamespaces(data))
      .catch(() => setNamespace('default'));
  }, [setNamespaces]);

  const options = namespaces.map((ns: string) => ({
    value: ns,
    label: ns
  }));

  //Rendering
  const Option = (props: any) => (
    <components.Option {...props}>
      <FaFolder style={{ marginRight: 8, color: '#7b8a97' }} />
      {props.data.label}
    </components.Option>
  );

  return (
    <Select
      classNamePrefix="namespace-dropdown"
      options={options}
      placeholder="Select a namespace"
      value={options.find((opt: any) => opt.value === namespace) || null}
      onChange={(option: { value: string } | null) => setNamespace(option ? option.value : '')}
      components={{ Option }}
      isClearable
    />
  );
};

export { NamespaceDropdown }; 